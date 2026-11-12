package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestLinkedWALCannotAliasMainMode(t *testing.T) {
	err := run([]string{"-linked", "-linked-wal", "-source", "deliberately-unread-source", "-propose"})
	if err == nil || !strings.Contains(err.Error(), "linked modes are mutually exclusive") {
		t.Fatalf("expected mode conflict, got %v", err)
	}
}

func TestWALInitializerDerivesOnlyROExistingFileState(t *testing.T) {
	src := `package sqlite3
func _winOpen(){
 libc.Xmemset(tls,pFile,0,uint64(96))
 (*TwinFile)(unsafe.Pointer(pFile)).Fh = uintptr(int64(-libc.Int32FromInt32(1)))
 _winGetTempname(tls,pVfs,0)
 (*Tsqlite3_file)(unsafe.Pointer(id)).FpMethods=v1
 (*TwinFile)(unsafe.Pointer(pFile)).FpVfs=pVfs
 (*TwinFile)(unsafe.Pointer(pFile)).Fh=h
 if isReadonly != 0 { v1=pFile+28; *(*Tu8)(unsafe.Pointer(v1))=Tu8(int32(*(*Tu8)(unsafe.Pointer(v1)))|libc.Int32FromInt32(WINFILE_RDONLY)) }
 if flags&int32(SQLITE_OPEN_MAIN_DB)!=0 && Xsqlite3_uri_boolean(tls,zName,0,int32(SQLITE_POWERSAFE_OVERWRITE))!=0 {v1=pFile+28}
 (*TwinFile)(unsafe.Pointer(pFile)).FlastErrno=uint32(0)
 (*TwinFile)(unsafe.Pointer(pFile)).FzPath=zName
 (*TwinFile)(unsafe.Pointer(pFile)).FhMap=libc.UintptrFromInt32(0)
 (*TwinFile)(unsafe.Pointer(pFile)).FpMapRegion=uintptr(0)
 (*TwinFile)(unsafe.Pointer(pFile)).FmmapSize=0
 (*TwinFile)(unsafe.Pointer(pFile)).FmmapSizeMax=_sqlite3Config.FszMmap
}`
	all, err := parseSource("fixture.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got, changes, err := walConstructor(all["_winOpen"])
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"func _paWinOpenExistingWALBound(", "requestFlags != int32(SQLITE_OPEN_READWRITE|SQLITE_OPEN_CREATE|SQLITE_OPEN_WAL)", "isReadonly := int32(SQLITE_OPEN_READONLY)", "FmmapSizeMax = 0", "int32(SQLITE_OPEN_READONLY)"} {
		if !strings.Contains(got, required) {
			t.Fatalf("missing %s: %s", required, got)
		}
	}
	for _, bad := range []string{"SQLITE_OPEN_MAIN_DB", "FpMethods =", "_winGetTempname", ".Fh = h", "_aSyscall", "CreateFile", "Shm"} {
		if strings.Contains(got, bad) {
			t.Fatal("forbidden", bad)
		}
	}
	if changes["wal-role-guard"].Count != 1 || changes["wal-readonly-state"].Count != 1 {
		t.Fatal(changes)
	}
	for _, mutation := range []string{
		strings.Replace(src, "libc.Xmemset(tls,pFile,0,uint64(96))", "", 1),
		strings.Replace(src, "libc.Xmemset(tls,pFile,0,uint64(96))", "libc.Xmemset(tls,pFile,0,uint64(96));libc.Xmemset(tls,pFile,0,uint64(96))", 1),
		strings.Replace(src, "uint64(96)", "uint64(88)", 1),
	} {
		a, e := parseSource("bad.go", []byte(mutation))
		if e != nil {
			t.Fatal(e)
		}
		if _, _, e = walConstructor(a["_winOpen"]); e == nil {
			t.Fatal("bad anchors accepted")
		}
	}
}

func TestWALManifestSeparatesDerivedInitializerAndLinkedDependency(t *testing.T) {
	raw, err := os.ReadFile("wal-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err = json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile("linked-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var linked manifest
	if err = json.Unmarshal(raw, &linked); err != nil {
		t.Fatal(err)
	}
	if m.WAL == nil || m.Linked != nil || len(m.Functions) != 0 || m.Version != "modernc.org/sqlite@v1.58.0-linked-wal-v1" {
		t.Fatal("derived function falsely inventoried", m)
	}
	d := m.WAL.DerivedInitializer
	if m.WAL.LinkedOutputSHA256 != linked.OutputSHA256 || d.SourceFunction != "_winOpen" || d.SourceFile != "lib/sqlite_windows.go" || d.Name != "_paWinOpenExistingWALBound" {
		t.Fatal("wrong derivation", d)
	}
	found := false
	for _, f := range linked.Functions {
		if f.Name == "_winOpen" {
			found = true
			if d.SourceSHA256 != f.SHA256 {
				t.Fatal("source drift")
			}
		}
	}
	if !found || len(d.OutputSHA256) != 64 || d.Transforms["complete-stock-open-to-wal-init-only"].OutputSHA256 != d.OutputSHA256 || d.Transforms["wal-request-identifier"].Count != 3 {
		t.Fatal("incomplete transform provenance")
	}
}
