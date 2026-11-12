package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractUsesASTNotCommentsOrStrings(t *testing.T) {
	src := []byte("package sqlite3\n// func _winA() {}\nfunc _winA(){ _winB(); _winB(); println(\"_winB()\"); _ = _aSyscall[int32(3)].FpCurrent }\nfunc _winB(){}\n")
	got, e := parseSource("fixture.go", src)
	if e != nil {
		t.Fatal(e)
	}
	if got["_winA"].Calls["_winB"] != 2 || got["_winA"].Syscalls["3"] != 1 {
		t.Fatalf("wrong AST counts: %+v", got["_winA"])
	}
	if !strings.HasPrefix(string(got["_winA"].body), "func _winA") {
		t.Fatal("comment included as declaration")
	}
}
func TestDuplicateDeclarationRefused(t *testing.T) {
	if _, e := parseSource("fixture.go", []byte("package p; func _winA(){}; func _winA(){}")); e == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestExactManifestRejectsCountHashAndMissingFunction(t *testing.T) {
	got, e := parseSource("fixture.go", []byte("package p; func _winA(){_winB()}; func _winB(){}"))
	if e != nil {
		t.Fatal(e)
	}
	expected := got["_winA"]
	expected.body = nil
	if e = matchFunction(expected, got["_winA"]); e != nil {
		t.Fatal(e)
	}
	expected.SHA256 = "wrong"
	if matchFunction(expected, got["_winA"]) == nil {
		t.Fatal("hash mismatch accepted")
	}
	expected = got["_winA"]
	expected.Calls = map[string]int{"_winB": 2}
	if matchFunction(expected, got["_winA"]) == nil {
		t.Fatal("count mismatch accepted")
	}
	if _, e = closure(got, []string{"_winMissing"}); e == nil {
		t.Fatal("missing function accepted")
	}
}
func TestClosureIncludesIndirectResourceHelpers(t *testing.T) {
	got, e := parseSource("fixture.go", []byte("package p; func _winOpen(){_winAccess()}; func _winAccess(){_winClose()}; func _winClose(){_winOpen()}"))
	if e != nil {
		t.Fatal(e)
	}
	c, e := closure(got, []string{"_winOpen"})
	if e != nil || len(c) != 3 {
		t.Fatalf("closure %v %v", c, e)
	}
}
func TestVariantRenamesOnlyASTIdentifiers(t *testing.T) {
	got, e := parseSource("fixture.go", []byte("package p; func _winA(){_winB();println(\"_winB\")}; func _winB(){}"))
	if e != nil {
		t.Fatal(e)
	}
	s, e := variant(got["_winA"], map[string]bool{"_winA": true, "_winB": true})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(s, "func _paWinA") || !strings.Contains(s, "_paWinB()") || !strings.Contains(s, `"_winB"`) {
		t.Fatal(s)
	}
}
func TestClosureIncludesFunctionValueReferences(t *testing.T) {
	all, e := parseSource("fixture.go", []byte("package p; func _winA(){ _ = _winB }; func _winB(){}"))
	if e != nil {
		t.Fatal(e)
	}
	got, e := closure(all, []string{"_winA"})
	if e != nil || len(got) != 2 {
		t.Fatalf("indirect function omitted: %v %v", got, e)
	}
}
func TestOriginalBytesAndCommentAreNotRewritten(t *testing.T) {
	src := []byte("package p; func _winA(){ /* _winB */ _winB() }; func _winB(){}")
	all, e := parseSource("fixture.go", src)
	if e != nil {
		t.Fatal(e)
	}
	before := string(all["_winA"].body)
	got, e := variant(all["_winA"], map[string]bool{"_winA": true, "_winB": true})
	if e != nil {
		t.Fatal(e)
	}
	if string(all["_winA"].body) != before || !strings.Contains(got, "/* _winB */") {
		t.Fatal("original bytes/comment rewritten")
	}
}

func TestManifestPinsInputAndOutputAndAllFunctionTransforms(t *testing.T) {
	b, e := os.ReadFile("manifest.json")
	if e != nil {
		t.Fatal(e)
	}
	var m manifest
	if e = json.Unmarshal(b, &m); e != nil {
		t.Fatal(e)
	}
	if m.OutputSHA256 == "" || len(m.Functions) != 29 || m.Inputs["LICENSE"] != licenseSHA {
		t.Fatalf("incomplete manifest: %+v", m)
	}
	for _, f := range m.Functions {
		if len(f.SHA256) != 64 || len(f.VariantSHA256) != 64 || f.Renames[f.Name] != f.Refs[f.Name] || f.Renames[f.Name] < 1 {
			t.Fatalf("unpinned function: %+v", f)
		}
		changed := f
		changed.VariantSHA256 = "wrong"
		if matchFunction(changed, f) == nil {
			t.Fatal("variant hash not checked")
		}
		changed = f
		changed.Syscalls = map[string]int{"999": 1}
		if matchFunction(changed, f) == nil {
			t.Fatal("syscall count not checked")
		}
	}
}
func TestOfficialInputMutationFailsBeforeOutput(t *testing.T) {
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "LICENSE"), []byte("tampered"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e := inventory(root); e == nil || !strings.Contains(e.Error(), "SHA mismatch") {
		t.Fatalf("wrong failure: %v", e)
	}
}
func TestActualGitAutocrlfCheckoutPreservesGeneratorBytes(t *testing.T) {
	root := t.TempDir()
	attrs, e := os.ReadFile(filepath.Join("..", "..", "..", ".gitattributes"))
	if e != nil {
		t.Fatal(e)
	}
	git := func(args ...string) {
		t.Helper()
		a := append([]string{"-c", "core.autocrlf=true", "-c", "core.safecrlf=false"}, args...)
		cmd := exec.Command("git", a...)
		cmd.Dir = root
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("git: %v %s", e, out)
		}
	}
	git("init", "-q")
	if e = os.WriteFile(filepath.Join(root, ".gitattributes"), attrs, 0600); e != nil {
		t.Fatal(e)
	}
	rel := filepath.Join("scripts", "sqlite-persistent-vfs", "generator")
	if e = os.MkdirAll(filepath.Join(root, rel), 0700); e != nil {
		t.Fatal(e)
	}
	original := map[string][]byte{}
	for _, name := range []string{"main.go", "manifest.json", "linked.go", "linked-manifest.json"} {
		b, e := os.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		original[name] = b
		if e = os.WriteFile(filepath.Join(root, rel, name), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	git("add", "--", ".gitattributes", filepath.ToSlash(rel))
	for name := range original {
		if e = os.Remove(filepath.Join(root, rel, name)); e != nil {
			t.Fatal(e)
		}
	}
	git("checkout-index", "--all", "--force")
	for name, want := range original {
		got, e := os.ReadFile(filepath.Join(root, rel, name))
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(got, []byte("\r")) || !bytes.Equal(got, want) {
			t.Fatalf("autocrlf changed %s bytes", name)
		}
	}
}

func TestCacheContainmentIncludesSiblingModulesNotJustSource(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	if !contained(cache, filepath.Join(cache, "other-module")) || !contained(cache, cache) {
		t.Fatal("cache sibling not excluded")
	}
	if contained(cache, filepath.Join(root, "cache-other")) {
		t.Fatal("lexical prefix mistaken for containment")
	}
}
