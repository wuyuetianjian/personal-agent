package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestLinkedRewriteUsesOriginalSyscallAndPrivateSleep(t *testing.T) {
	all, e := parseSource("fixture.go", []byte(`package sqlite3
func _winRead(){ _winFileSize(); _ = _aSyscall[int32(52)].FpCurrent; Xsqlite3_win32_sleep(tls,1) }
func _winFileSize(){}`))
	if e != nil {
		t.Fatal(e)
	}
	got, _, e := linkedVariant(all["_winRead"], map[string]bool{"_winRead": true, "_winFileSize": true}, map[string]string{"52": "libc.XReadFile", "55": "libc.XSleep"})
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(got, "_aSyscall") || strings.Contains(got, "Xsqlite3_win32_sleep") || !strings.Contains(got, "__ccgo_fp(libc.XReadFile)") || !strings.Contains(got, "libc.XSleep(tls, 1)") {
		t.Fatal(got)
	}
}
func TestLinkedUnknownSyscallRefuses(t *testing.T) {
	all, e := parseSource("f.go", []byte(`package p;func _winRead(){_= _aSyscall[int32(999)].FpCurrent}`))
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = linkedVariant(all["_winRead"], map[string]bool{"_winRead": true}, map[string]string{}); e == nil {
		t.Fatal("unclassified syscall accepted")
	}
}
func TestBoundConstructorRequiresEveryStockAnchor(t *testing.T) {
	all, e := parseSource("f.go", []byte(`package p;func _winOpen(){}`))
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = boundConstructor(all["_winOpen"]); e == nil {
		t.Fatal("missing constructor anchors accepted")
	}
}
func TestBoundConstructorKeepsStockFieldsButNoAcquisitionOrMethods(t *testing.T) {
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
	all, e := parseSource("fixture.go", []byte(src))
	if e != nil {
		t.Fatal(e)
	}
	got, changes, e := boundConstructor(all["_winOpen"])
	if e != nil {
		t.Fatal(e)
	}
	for _, forbidden := range []string{"FpMethods =", "_winGetTempname", "SQLITE_POWERSAFE_OVERWRITE", "FszMmap", ".Fh = h"} {
		if strings.Contains(got, forbidden) {
			t.Fatal(forbidden)
		}
	}
	if !strings.Contains(got, "FmmapSizeMax = 0") || changes["Fh-invalid-keep-bound-drop"].Count != 2 || changes["FpMethods-remove"].Count != 1 {
		t.Fatal(got, changes)
	}
	bad := strings.Replace(src, "(*TwinFile)(unsafe.Pointer(pFile)).FhMap=libc.UintptrFromInt32(0)", "", 1)
	all, e = parseSource("bad.go", []byte(bad))
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = boundConstructor(all["_winOpen"]); e == nil {
		t.Fatal("missing stock tail accepted")
	}
}
func TestOriginalSyscallInitMappingIsUnique(t *testing.T) {
	src := `package p;func init(){p:=unsafe.Pointer(&_aSyscall);*(*uintptr)(unsafe.Add(p,1256))=__ccgo_fp(libc.XReadFile)}`
	m, h, e := syscallTargets([]byte(src))
	if e != nil || m["52"] != "libc.XReadFile" || len(h) != 64 {
		t.Fatal(m, h, e)
	}
	if _, _, e = syscallTargets([]byte(src + `;func init(){p:=unsafe.Pointer(&_aSyscall)}`)); e == nil {
		t.Fatal("duplicate source init accepted")
	}
	if _, _, e = syscallTargets([]byte(strings.Replace(src, "1256", "1257", 1))); e == nil {
		t.Fatal("unaligned slot accepted")
	}
}

func TestLinkedManifestHasCompleteConstructorAndDirectTargets(t *testing.T) {
	raw, e := os.ReadFile("linked-manifest.json")
	if e != nil {
		t.Fatal(e)
	}
	var m manifest
	if e = json.Unmarshal(raw, &m); e != nil {
		t.Fatal(e)
	}
	if m.Linked == nil || len(m.Functions) != 17 || len(m.Linked.SyscallTargets) != 14 || len(m.Linked.Transforms) != 17 {
		t.Fatal("incomplete linked manifest")
	}
	if m.Linked.SyscallTargets["52"] != "libc.XReadFile" || m.Linked.SyscallTargets["55"] != "libc.XSleep" {
		t.Fatal("mutable/unknown native targets")
	}
	for k := range m.Linked.ExternalIdentifiers {
		if k == "_aSyscall" || k == "_winIoMethod" || k == "_winShmNodeList" || k == "Xsqlite3_win32_sleep" {
			t.Fatal("unsafe residual", k)
		}
	}
	if m.Linked.Transforms["_winOpen"]["Fh-invalid-keep-bound-drop"].Count != 2 || m.Linked.Transforms["_winOpen"]["PSOW-remove"].Count != 1 {
		t.Fatal("constructor transform missing")
	}
}
