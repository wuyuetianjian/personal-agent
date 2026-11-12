// Command generator produces review-only text, never an accepting VFS.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

type function struct {
	File, Name, SHA256, VariantSHA256 string
	Calls, Refs, Syscalls, Renames    map[string]int
	body                              []byte
}
type manifest struct {
	Version      string
	Inputs       map[string]string
	Roots        []string
	Functions    []function
	OutputSHA256 string
}

var pins = map[string]string{
	"LICENSE":                          "c6fe05491a60ae13bcd223088d2705e36dede24e5587226231d2459ada5c4822",
	"sqlite.go":                        "52fefb00e9f7a9a1bad23b034b57ce47b156bf29bebe7c2aef01083f5880759e",
	"lib/sqlite.go":                    "8210957d4836c361efb18808fb6fd4dc5f2a7ca362fd83881d6018e9913ee4d6",
	"lib/sqlite_windows.go":            "414fac0347bda229e81decf8b765a276dc59f382cde33542f7246862e7022d98",
	"lib/sqlite_g_0000000000060000.go": "8416138b3519a8ad4a2e4a76928c34781333915f09ef5d5444eb75d5cc4eabe7",
}

const licenseSHA = "c6fe05491a60ae13bcd223088d2705e36dede24e5587226231d2459ada5c4822"

var roots = []string{"_winOpen", "_winRead", "_winFileSize", "_winLock", "_winUnlock", "_winCheckReservedLock", "_winClose"}

func hash(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func parseSource(name string, src []byte) (map[string]function, error) {
	fset := token.NewFileSet()
	f, e := parser.ParseFile(fset, name, src, 0)
	if e != nil {
		return nil, e
	}
	out := map[string]function{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || !strings.HasPrefix(fn.Name.Name, "_win") {
			continue
		}
		n := fn.Name.Name
		if _, ok := out[n]; ok {
			return nil, fmt.Errorf("duplicate declaration %s", n)
		}
		body := src[fset.Position(fn.Pos()).Offset:fset.Position(fn.End()).Offset]
		v := function{File: name, Name: n, SHA256: hash(body), Calls: map[string]int{}, Refs: map[string]int{}, Syscalls: map[string]int{}, body: body}
		ast.Inspect(fn, func(node ast.Node) bool {
			switch x := node.(type) {
			case *ast.Ident:
				if strings.HasPrefix(x.Name, "_win") {
					v.Refs[x.Name]++
				}
			case *ast.CallExpr:
				if id, ok := x.Fun.(*ast.Ident); ok && strings.HasPrefix(id.Name, "_win") {
					v.Calls[id.Name]++
				}
			case *ast.IndexExpr:
				if id, ok := x.X.(*ast.Ident); ok && id.Name == "_aSyscall" {
					var b bytes.Buffer
					_ = format.Node(&b, fset, x.Index)
					key := b.String()
					if c, ok := x.Index.(*ast.CallExpr); ok && len(c.Args) == 1 {
						if lit, ok := c.Args[0].(*ast.BasicLit); ok {
							key = lit.Value
						}
					}
					v.Syscalls[key]++
				}
			}
			return true
		})
		out[n] = v
	}
	return out, nil
}
func matchFunction(want, got function) error {
	want.body = nil
	got.body = nil
	if !reflect.DeepEqual(want, got) {
		return fmt.Errorf("function declaration/hash/call/ref/syscall count mismatch: %s", want.Name)
	}
	return nil
}
func closure(all map[string]function, start []string) ([]string, error) {
	seen := map[string]bool{}
	queue := append([]string{}, start...)
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if seen[n] {
			continue
		}
		v, ok := all[n]
		if !ok {
			return nil, fmt.Errorf("missing transitive function %s", n)
		}
		seen[n] = true
		for name := range v.Calls {
			queue = append(queue, name)
		}
		for name := range v.Refs {
			if _, ok := all[name]; ok {
				queue = append(queue, name)
			}
		}
	}
	result := make([]string, 0, len(seen))
	for n := range seen {
		result = append(result, n)
	}
	sort.Strings(result)
	return result, nil
}
func variant(v function, selected map[string]bool) (string, error) {
	prefix := "package sqlite3\n"
	src := append([]byte(prefix), v.body...)
	fs := token.NewFileSet()
	f, e := parser.ParseFile(fs, v.File, src, 0)
	if e != nil {
		return "", e
	}
	type edit struct {
		start, end int
		value      string
	}
	var edits []edit
	ast.Inspect(f, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && selected[id.Name] {
			edits = append(edits, edit{fs.Position(id.Pos()).Offset, fs.Position(id.End()).Offset, "_paWin" + strings.TrimPrefix(id.Name, "_win")})
		}
		return true
	})
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	for _, x := range edits {
		src = append(append(append([]byte{}, src[:x.start]...), []byte(x.value)...), src[x.end:]...)
	}
	formatted, e := format.Source(src)
	if e != nil {
		return "", e
	}
	return strings.TrimPrefix(string(formatted), "package sqlite3\n\n"), nil
}
func inventory(root string) (manifest, []byte, error) {
	m := manifest{Version: "modernc.org/sqlite@v1.58.0-review-only-v1", Inputs: pins, Roots: roots}
	all := map[string]function{}
	keys := make([]string, 0, len(pins))
	for n := range pins {
		keys = append(keys, n)
	}
	sort.Strings(keys)
	for _, n := range keys {
		data, e := os.ReadFile(filepath.Join(root, n))
		if e != nil {
			return m, nil, e
		}
		if hash(data) != pins[n] {
			return m, nil, fmt.Errorf("official input SHA mismatch: %s", n)
		}
		if !strings.HasPrefix(n, "lib/") {
			continue
		}
		items, e := parseSource(n, data)
		if e != nil {
			return m, nil, e
		}
		for k, v := range items {
			if _, ok := all[k]; ok {
				return m, nil, fmt.Errorf("duplicate source function %s", k)
			}
			all[k] = v
		}
	}
	names, e := closure(all, roots)
	if e != nil {
		return m, nil, e
	}
	selected := map[string]bool{}
	for _, n := range names {
		selected[n] = true

	}
	license, e := os.ReadFile(filepath.Join(root, "LICENSE"))
	if e != nil {
		return m, nil, e
	}
	if hash(license) != licenseSHA {
		return m, nil, errors.New("official LICENSE SHA mismatch")
	}
	var b strings.Builder
	b.WriteString("// REVIEW ONLY. NOT COMPILABLE OR LINKABLE. No registration or IO authorization.\n// Source: modernc.org/sqlite v1.58.0. Variants contain unsafe stock paths pending replacement.\n")
	for _, line := range strings.Split(string(license), "\n") {
		b.WriteString("// " + line + "\n")
	}
	for _, n := range names {
		s, e := variant(all[n], selected)
		if e != nil {
			return m, nil, e
		}
		v := all[n]
		v.VariantSHA256 = hash([]byte(s))
		v.Renames = map[string]int{}
		for id, count := range v.Refs {
			if selected[id] {
				v.Renames[id] = count
			}
		}
		m.Functions = append(m.Functions, v)
		fmt.Fprintf(&b, "\n// %s input SHA256 %s\n%s", v.File, v.SHA256, s)
	}
	output := []byte(b.String())
	m.OutputSHA256 = hash(output)
	return m, output, nil
}
func run(args []string) error {
	f := flag.NewFlagSet("generator", flag.ContinueOnError)
	source := f.String("source", "", "verified official module directory")
	approved := f.String("manifest", "", "reviewed manifest JSON")
	cacheRoot := f.String("cache-root", "", "actual GOMODCACHE from the verified runner")
	out := f.String("out", "", "new review .go.txt file outside source")
	propose := f.Bool("propose", false, "print proposed inventory only; no output file")
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 || *source == "" {
		return errors.New("explicit source required; no positional arguments")
	}
	actual, output, e := inventory(*source)
	if e != nil {
		return e
	}
	if *propose {
		if *approved != "" || *out != "" {
			return errors.New("proposal cannot generate output")
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(actual)
	}
	if *approved == "" || *cacheRoot == "" || !strings.HasSuffix(*out, ".go.txt") {
		return errors.New("reviewed manifest, cache-root and .go.txt output required")
	}
	raw, e := os.ReadFile(*approved)
	if e != nil {
		return e
	}
	var want manifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if e = dec.Decode(&want); e != nil {
		return e
	}
	if e = dec.Decode(new(any)); e != io.EOF {
		return errors.New("manifest trailing data")
	}
	if want.Version != actual.Version || !reflect.DeepEqual(want.Inputs, actual.Inputs) || !reflect.DeepEqual(want.Roots, actual.Roots) || want.OutputSHA256 != actual.OutputSHA256 || len(want.Functions) != len(actual.Functions) {
		return errors.New("manifest identity/closure/output mismatch")
	}
	for i := range want.Functions {
		if e = matchFunction(want.Functions[i], actual.Functions[i]); e != nil {
			return e
		}
	}
	src, e := filepath.EvalSymlinks(*source)
	if e != nil {
		return e
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(*out))
	if e != nil {
		return e
	}
	src, _ = filepath.Abs(src)
	parent, _ = filepath.Abs(parent)
	rel, e := filepath.Rel(src, parent)
	if e != nil {
		return e
	}
	if rel == "." || (!filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return errors.New("output must be outside official source")
	}
	cache, e := filepath.EvalSymlinks(*cacheRoot)
	if e != nil {
		return e
	}
	cache, _ = filepath.Abs(cache)
	if contained(cache, parent) {
		return errors.New("output must be outside entire GOMODCACHE")
	}
	file, e := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, writeErr := file.Write(output)
	return errors.Join(writeErr, file.Close())
}
func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}

func contained(parent, child string) bool {
	rel, e := filepath.Rel(parent, child)
	return e == nil && (rel == "." || (!filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}
