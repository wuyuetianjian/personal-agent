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
	"strings"
)

type derivedInitializer struct {
	Name, SourceFile, SourceFunction, SourceSHA256, OutputSHA256 string
	Transforms                                                   map[string]transform
}
type walDetails struct {
	LinkedOutputSHA256 string
	DerivedInitializer derivedInitializer
}

// First extract the same audited official _winOpen field anchors as main.
// Then transform only the synthetic constructor's name, request and RO state.
// The original declaration remains one upstream function, not a new discovery.
func walConstructor(v function) (string, map[string]transform, error) {
	base, changes, err := boundConstructor(v)
	if err != nil {
		return "", nil, err
	}
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, "initializer.go", "package sqlite3\n"+base, 0)
	if err != nil {
		return "", nil, err
	}
	record := func(key, before, after string) {
		old := changes[key]
		if old.Count != 0 {
			return
		}
		changes[key] = transform{1, hash([]byte(before)), hash([]byte(after))}
	}
	fn := f.Decls[0].(*ast.FuncDecl)
	record("wal-name", fn.Name.Name, "_paWinOpenExistingWALBound")
	fn.Name.Name = "_paWinOpenExistingWALBound"
	renamed := 0
	ast.Inspect(fn, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == "flags" {
			id.Name = "requestFlags"
			renamed++
		}
		return true
	})
	changes["wal-request-identifier"] = transform{renamed, hash([]byte(strings.Repeat("flags\x00", renamed))), hash([]byte(strings.Repeat("requestFlags\x00", renamed)))}
	guards, states := 0, 0
	ast.Inspect(fn, func(n ast.Node) bool {
		if x, ok := n.(*ast.BinaryExpr); ok && nodeText(fs, x) == "requestFlags != int32(SQLITE_OPEN_READONLY|SQLITE_OPEN_MAIN_DB)" {
			before := nodeText(fs, x)
			x.Y, _ = parser.ParseExpr("int32(SQLITE_OPEN_READWRITE|SQLITE_OPEN_CREATE|SQLITE_OPEN_WAL)")
			record("wal-role-guard", before, nodeText(fs, x))
			guards++
		}
		if x, ok := n.(*ast.AssignStmt); ok && len(x.Lhs) == 1 && nodeText(fs, x.Lhs[0]) == "isReadonly" {
			before := nodeText(fs, x)
			x.Rhs[0], _ = parser.ParseExpr("int32(SQLITE_OPEN_READONLY)")
			record("wal-readonly-state", before, nodeText(fs, x))
			states++
		}
		return true
	})
	if guards != 1 || states != 1 || renamed != 3 {
		return "", nil, fmt.Errorf("WAL constructor shape drift: guards=%d states=%d identifiers=%d", guards, states, renamed)
	}
	var out bytes.Buffer
	if err = format.Node(&out, token.NewFileSet(), fn); err != nil {
		return "", nil, err
	}
	result := out.String() + "\n"
	changes["complete-stock-open-to-wal-init-only"] = transform{1, v.SHA256, hash([]byte(result))}
	return result, changes, nil
}

func walInventory(root string) (manifest, []byte, error) {
	linked, _, err := linkedInventory(root)
	if err != nil {
		return manifest{}, nil, err
	}
	var source function
	for _, v := range linked.Functions {
		if v.Name == "_winOpen" {
			source = v
		}
	}
	if source.Name == "" {
		return manifest{}, nil, fmt.Errorf("missing official _winOpen")
	}
	text, changes, err := walConstructor(source)
	if err != nil {
		return manifest{}, nil, err
	}
	license, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		return manifest{}, nil, err
	}
	var b strings.Builder
	b.WriteString("//go:build windows && (amd64 || arm64) && nativeprobe_sqlite_persistent && nativeprobe_sqlite_main_read && nativeprobe_sqlite_existing_wal\n\n// Generated from pinned official SQLite. Existing-WAL initializer only. No VFS registration.\n")
	for _, line := range strings.Split(string(license), "\n") {
		b.WriteString("// " + line + "\n")
	}
	b.WriteString("\npackage sqlite3\n\nimport (\"unsafe\"; \"modernc.org/libc\")\n\n" + text)
	output, err := format.Source([]byte(b.String()))
	if err != nil {
		return manifest{}, nil, err
	}
	m := manifest{Version: "modernc.org/sqlite@v1.58.0-linked-wal-v1", Inputs: pins, Roots: []string{"_winOpen"}, Functions: []function{}, OutputSHA256: hash(output), WAL: &walDetails{LinkedOutputSHA256: linked.OutputSHA256, DerivedInitializer: derivedInitializer{Name: "_paWinOpenExistingWALBound", SourceFile: source.File, SourceFunction: source.Name, SourceSHA256: source.SHA256, OutputSHA256: hash([]byte(text)), Transforms: changes}}}
	return m, output, nil
}
