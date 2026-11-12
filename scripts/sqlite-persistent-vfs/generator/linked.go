package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type transform struct {
	Count                     int
	InputSHA256, OutputSHA256 string
}
type linkedDetails struct {
	SyscallTargets             map[string]string
	SyscallInitSHA256          string
	Transforms                 map[string]map[string]transform
	Calls, ExternalIdentifiers map[string]int
}

func nodeText(fs *token.FileSet, n ast.Node) string {
	var b bytes.Buffer
	_ = format.Node(&b, fs, n)
	return b.String()
}
func functionAST(v function) (*token.FileSet, *ast.File, []byte, error) {
	src := append([]byte("package sqlite3\n"), v.body...)
	fs := token.NewFileSet()
	f, e := parser.ParseFile(fs, v.File, src, parser.ParseComments)
	return fs, f, src, e
}
func linkedVariant(v function, selected map[string]bool, targets map[string]string) (string, map[string]transform, error) {
	fs, f, src, e := functionAST(v)
	if e != nil {
		return "", nil, e
	}
	type edit struct {
		a, b       int
		value, key string
	}
	var edits []edit
	var failure error
	add := func(n ast.Node, value, key string) {
		edits = append(edits, edit{fs.Position(n.Pos()).Offset, fs.Position(n.End()).Offset, value, key})
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok && s.Sel.Name == "FpCurrent" {
			if x, ok := s.X.(*ast.IndexExpr); ok {
				if id, ok := x.X.(*ast.Ident); ok && id.Name == "_aSyscall" {
					key := ""
					if lit, ok := x.Index.(*ast.BasicLit); ok {
						key = lit.Value
					}
					if c, ok := x.Index.(*ast.CallExpr); ok && len(c.Args) == 1 {
						if lit, ok := c.Args[0].(*ast.BasicLit); ok {
							key = lit.Value
						}
					}
					target, ok := targets[key]
					if !ok {
						failure = fmt.Errorf("unclassified syscall %s", nodeText(fs, x))
						return false
					}
					add(s, "__ccgo_fp("+target+")", "syscall:"+key)
					return false
				}
			}
		}
		if id, ok := n.(*ast.Ident); ok {
			if selected[id.Name] {
				add(id, "_paWin"+strings.TrimPrefix(id.Name, "_win"), "rename:"+id.Name)
			}
			if id.Name == "_winIoerrRetry" || id.Name == "_winIoerrRetryDelay" {
				add(id, "_paWin"+strings.TrimPrefix(id.Name, "_win"), "private-config:"+id.Name)
			}
			if id.Name == "Xsqlite3_win32_sleep" {
				if targets["55"] != "libc.XSleep" {
					failure = fmt.Errorf("unverified sleep target")
				} else {
					add(id, "libc.XSleep", "sleep:55")
				}
			}
		}
		return true
	})
	if failure != nil {
		return "", nil, failure
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].a < edits[j].a })
	counts := map[string]int{}
	inputs, outputs := map[string]string{}, map[string]string{}
	for i, x := range edits {
		if i > 0 && edits[i-1].b > x.a {
			return "", nil, fmt.Errorf("overlapping AST edits")
		}
		counts[x.key]++
		inputs[x.key] += string(src[x.a:x.b]) + "\x00"
		outputs[x.key] += x.value + "\x00"
	}
	for i := len(edits) - 1; i >= 0; i-- {
		x := edits[i]
		src = append(append(append([]byte{}, src[:x.a]...), []byte(x.value)...), src[x.b:]...)
	}
	formatted, e := format.Source(src)
	if e != nil {
		return "", nil, e
	}
	result := strings.TrimPrefix(string(formatted), "package sqlite3\n\n")
	if strings.Contains(result, "_aSyscall") || strings.Contains(result, "Xsqlite3_win32_sleep") {
		return "", nil, fmt.Errorf("mutable stock syscall chain remains")
	}
	changes := map[string]transform{}
	for k, n := range counts {
		changes[k] = transform{n, hash([]byte(inputs[k])), hash([]byte(outputs[k]))}
	}
	return result, changes, nil
}
func boundConstructor(v function) (string, map[string]transform, error) {
	fs, f, _, e := functionAST(v)
	if e != nil {
		return "", nil, e
	}
	fn := f.Decls[0].(*ast.FuncDecl)
	anchors := map[string][]string{}
	for _, stmt := range fn.Body.List {
		text := nodeText(fs, stmt)
		switch x := stmt.(type) {
		case *ast.ExprStmt:
			if call, ok := x.X.(*ast.CallExpr); ok && nodeText(fs, call.Fun) == "libc.Xmemset" {
				anchors["memset"] = append(anchors["memset"], text)
			}
		case *ast.AssignStmt:
			if len(x.Lhs) == 1 {
				if field, ok := x.Lhs[0].(*ast.SelectorExpr); ok {
					anchors[field.Sel.Name] = append(anchors[field.Sel.Name], text)
				}
			}
		case *ast.IfStmt:
			if nodeText(fs, x.Cond) == "isReadonly != 0" {
				anchors["readonly"] = append(anchors["readonly"], text)
			}
			if strings.Contains(nodeText(fs, x.Cond), "SQLITE_POWERSAFE_OVERWRITE") {
				anchors["PSOW"] = append(anchors["PSOW"], text)
			}
		}
	}
	required := []string{"memset", "FpVfs", "readonly", "PSOW", "FpMethods", "FlastErrno", "FzPath", "FhMap", "FpMapRegion", "FmmapSize", "FmmapSizeMax"}
	for _, k := range required {
		if len(anchors[k]) != 1 {
			return "", nil, fmt.Errorf("constructor anchor %s count=%d", k, len(anchors[k]))
		}
	}
	if len(anchors["Fh"]) != 2 || anchors["Fh"][1] != "(*TwinFile)(unsafe.Pointer(pFile)).Fh = h" {
		return "", nil, fmt.Errorf("constructor handle anchors changed")
	}
	if anchors["memset"][0] != "libc.Xmemset(tls, pFile, 0, uint64(96))" || anchors["FmmapSizeMax"][0] != "(*TwinFile)(unsafe.Pointer(pFile)).FmmapSizeMax = _sqlite3Config.FszMmap" {
		return "", nil, fmt.Errorf("constructor size/mmap shape changed")
	}
	changes := map[string]transform{}
	keep := func(k string) string {
		s := anchors[k][0]
		changes[k] = transform{1, hash([]byte(s)), hash([]byte(s))}
		return s + "\n"
	}
	var body strings.Builder
	body.WriteString(`func _paWinOpenBound(tls *libc.TLS, pVfs, zName, file uintptr, flags int32, pOutFlags uintptr) int32 {
 if pOutFlags != 0 { *(*int32)(unsafe.Pointer(pOutFlags)) = 0 }
 if file == 0 || pVfs == 0 || zName == 0 || flags != int32(SQLITE_OPEN_READONLY|SQLITE_OPEN_MAIN_DB) || unsafe.Sizeof(TwinFile{}) != 96 { return SQLITE_CANTOPEN }
 pFile := file
 isReadonly := flags & int32(SQLITE_OPEN_READONLY)
 var v1 uintptr
`)
	body.WriteString(keep("memset"))
	body.WriteString(anchors["Fh"][0] + "\n")
	changes["Fh-invalid-keep-bound-drop"] = transform{2, hash([]byte(strings.Join(anchors["Fh"], "\n"))), hash([]byte(anchors["Fh"][0]))}
	body.WriteString(keep("FpVfs"))
	body.WriteString(keep("readonly"))
	for _, k := range []string{"FlastErrno", "FzPath", "FhMap", "FpMapRegion", "FmmapSize"} {
		body.WriteString(keep(k))
	}
	rewritten := "(*TwinFile)(unsafe.Pointer(pFile)).FmmapSizeMax = 0"
	body.WriteString(rewritten + "\n")
	changes["mmap-disable"] = transform{1, hash([]byte(anchors["FmmapSizeMax"][0])), hash([]byte(rewritten))}
	for _, k := range []string{"PSOW", "FpMethods"} {
		changes[k+"-remove"] = transform{1, hash([]byte(anchors[k][0])), hash(nil)}
	}
	body.WriteString("if pOutFlags != 0 { *(*int32)(unsafe.Pointer(pOutFlags)) = int32(SQLITE_OPEN_READONLY) };return SQLITE_OK\n}\n")
	result, e := format.Source([]byte("package sqlite3\n" + body.String()))
	if e != nil {
		return "", nil, e
	}
	text := strings.TrimPrefix(string(result), "package sqlite3\n\n")
	changes["complete-stock-open-to-init-only"] = transform{1, v.SHA256, hash([]byte(text))}
	return text, changes, nil
}

// Recover original syscall targets from the uniquely identified official init
// block, never from runtime FpCurrent or initially-zero FpDefault slots.
func syscallTargets(source []byte) (map[string]string, string, error) {
	fs := token.NewFileSet()
	f, e := parser.ParseFile(fs, "sqlite_windows.go", source, 0)
	if e != nil {
		return nil, "", e
	}
	targets := map[string]string{}
	count := 0
	var span string
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "init" || len(fn.Body.List) == 0 {
			continue
		}
		if nodeText(fs, fn.Body.List[0]) != "p := unsafe.Pointer(&_aSyscall)" {
			continue
		}
		count++
		span = nodeText(fs, fn)
		for _, s := range fn.Body.List[1:] {
			a, ok := s.(*ast.AssignStmt)
			if !ok || len(a.Lhs) != 1 || len(a.Rhs) != 1 {
				return nil, "", fmt.Errorf("unexpected syscall init statement")
			}
			rhs, ok := a.Rhs[0].(*ast.CallExpr)
			if !ok || nodeText(fs, rhs.Fun) != "__ccgo_fp" || len(rhs.Args) != 1 {
				return nil, "", fmt.Errorf("unexpected syscall target")
			}
			target := nodeText(fs, rhs.Args[0])
			offset := -1
			ast.Inspect(a.Lhs[0], func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok && nodeText(fs, call.Fun) == "unsafe.Add" && len(call.Args) == 2 {
					if lit, ok := call.Args[1].(*ast.BasicLit); ok {
						offset, _ = strconv.Atoi(lit.Value)
					}
				}
				return true
			})
			if offset < 8 || (offset-8)%24 != 0 {
				return nil, "", fmt.Errorf("unexpected syscall slot offset %d", offset)
			}
			key := strconv.Itoa((offset - 8) / 24)
			if _, ok := targets[key]; ok {
				return nil, "", fmt.Errorf("duplicate syscall slot %s", key)
			}
			targets[key] = target
		}
	}
	if count != 1 {
		return nil, "", fmt.Errorf("syscall init count %d", count)
	}
	return targets, hash([]byte(span)), nil
}
func linkedInventory(root string) (manifest, []byte, error) {
	base, _, e := inventory(root)
	if e != nil {
		return manifest{}, nil, e
	}
	all := map[string]function{}
	for _, v := range base.Functions {
		all[v.Name] = v
	}
	selectedRoots := []string{"_winRead", "_winFileSize", "_winLock", "_winUnlock", "_winCheckReservedLock"}
	names, e := closure(all, selectedRoots)
	if e != nil {
		return manifest{}, nil, e
	}
	selected := map[string]bool{}
	for _, n := range names {
		selected[n] = true
	}
	data, e := os.ReadFile(filepath.Join(root, "lib/sqlite_windows.go"))
	if e != nil {
		return manifest{}, nil, e
	}
	targets, initHash, e := syscallTargets(data)
	if e != nil {
		return manifest{}, nil, e
	}
	m := manifest{Version: "modernc.org/sqlite@v1.58.0-linked-main-v1", Inputs: pins, Roots: append([]string{"_winOpen"}, selectedRoots...), Linked: &linkedDetails{SyscallTargets: map[string]string{}, SyscallInitSHA256: initHash, Transforms: map[string]map[string]transform{}}}
	license, e := os.ReadFile(filepath.Join(root, "LICENSE"))
	if e != nil {
		return m, nil, e
	}
	var b strings.Builder
	b.WriteString("//go:build windows && (amd64 || arm64) && nativeprobe_sqlite_persistent && nativeprobe_sqlite_main_read\n\n// Generated from pinned official SQLite. Experimental main-only helpers. No VFS registration.\n")
	for _, s := range strings.Split(string(license), "\n") {
		b.WriteString("// " + s + "\n")
	}
	b.WriteString("\npackage sqlite3\n\nimport (\"unsafe\"; \"modernc.org/libc\")\n\nconst _paWinIoerrRetry = int32(SQLITE_WIN32_IOERR_RETRY)\nconst _paWinIoerrRetryDelay = int32(SQLITE_WIN32_IOERR_RETRY_DELAY)\n\n")
	constructor, edits, e := boundConstructor(all["_winOpen"])
	if e != nil {
		return m, nil, e
	}
	b.WriteString(constructor)
	m.Linked.Transforms["_winOpen"] = edits
	cv := all["_winOpen"]
	cv.VariantSHA256 = hash([]byte(constructor))
	cv.Renames = nil
	m.Functions = append(m.Functions, cv)
	for _, n := range names {
		v := all[n]
		s, edits, e := linkedVariant(v, selected, targets)
		if e != nil {
			return m, nil, e
		}
		b.WriteString("\n" + s)
		m.Linked.Transforms[n] = edits
		v.VariantSHA256 = hash([]byte(s))
		v.Renames = map[string]int{}
		for id, count := range v.Refs {
			if selected[id] {
				v.Renames[id] = count
			}
		}
		m.Functions = append(m.Functions, v)
		for key := range v.Syscalls {
			m.Linked.SyscallTargets[key] = targets[key]
		}
		if _, ok := edits["sleep:55"]; ok {
			m.Linked.SyscallTargets["55"] = targets["55"]
		}
	}
	output, e := format.Source([]byte(b.String()))
	if e != nil {
		return m, nil, e
	}
	fs := token.NewFileSet()
	f, e := parser.ParseFile(fs, "generated.go", output, 0)
	if e != nil {
		return m, nil, e
	}
	m.Linked.Calls = map[string]int{}
	m.Linked.ExternalIdentifiers = map[string]int{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			m.Linked.Calls[nodeText(fs, x.Fun)]++
		case *ast.Ident:
			if x.Obj == nil {
				m.Linked.ExternalIdentifiers[x.Name]++
			}
		}
		return true
	})
	for _, bad := range []string{"_aSyscall", "_winIoMethod", "_winShmNodeList", "Xsqlite3_win32_sleep", "libc.XCreateFile", "libc.XCloseHandle", "libc.XDeleteFile", "libc.XMapView", "libc.XUnmap"} {
		if bytes.Contains(output, []byte(bad)) {
			return m, nil, fmt.Errorf("forbidden linked dependency %s", bad)
		}
	}
	m.OutputSHA256 = hash(output)
	return m, output, nil
}
