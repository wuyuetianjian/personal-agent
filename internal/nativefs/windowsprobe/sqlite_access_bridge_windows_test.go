//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_lease && nativeprobe_sqlite_persistent && nativeprobe_sqlite_main_read && nativeprobe_sqlite_access

package windowsprobe

import (
	"bytes"
	"database/sql"
	"errors"
	"golang.org/x/sys/windows"
	"modernc.org/libc"
	_ "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
	"os"
	"path/filepath"
	"testing"
	"unsafe"
)

func accessCall(t *testing.T, v uintptr, name string, flag int32) (int32, int32) {
	t.Helper()
	tls := libc.NewTLS()
	defer tls.Close()
	n, e := libc.CString(name)
	if e != nil {
		t.Fatal(e)
	}
	defer libc.Xfree(tls, n)
	out := int32(-1)
	p := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(v))
	fn := *(*func(*libc.TLS, uintptr, uintptr, int32, uintptr) int32)(unsafe.Pointer(&p.FxAccess))
	rc := fn(tls, v, n, flag, uintptr(unsafe.Pointer(&out)))
	return rc, out
}
func accessFixture(t *testing.T, realWAL bool) (string, *persistentAccessProbe, uintptr, uintptr) {
	t.Helper()
	dir := localRootPath(t)
	name := filepath.Join(dir, "access.db")
	if realWAL {
		db, e := sql.Open("sqlite", name)
		if e != nil {
			t.Fatal(e)
		}
		db.SetMaxOpenConns(1)
		t.Cleanup(func() {
			if e := db.Close(); e != nil {
				t.Error(e)
			}
		})
		var mode string
		if e = db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode); e != nil || mode != "wal" {
			t.Fatalf("stock setup %s %v", mode, e)
		}
		for _, q := range []string{"PRAGMA wal_autocheckpoint=0", "CREATE TABLE fixture(v)", "INSERT INTO fixture VALUES(7)"} {
			if _, e = db.Exec(q); e != nil {
				t.Fatal(e)
			}
		}
		var n int
		if e = db.QueryRow("SELECT v FROM fixture").Scan(&n); e != nil || n != 7 {
			t.Fatalf("stock SQL %d %v", n, e)
		}
	} else {
		if e := os.WriteFile(name, []byte("synthetic metadata fixture"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	root, e := OpenLocalRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := root.Close(); e != nil {
			t.Error(e)
		}
	})
	a, e := newPersistentAccessProbe(root, name)
	if e != nil {
		t.Fatalf("access constructor setup: %v", e)
	}
	v, e := sqlite3.ExperimentalAccessRegister(a, name)
	if e != nil {
		t.Fatal(e)
	}
	tls := libc.NewTLS()
	defer tls.Close()
	stock := sqlite3.Xsqlite3_vfs_find(tls, 0)
	if stock == v || stock == 0 {
		t.Fatal("stock VFS lost")
	}
	return name, a, v, stock
}
func accessControl(t *testing.T, a *persistentAccessProbe, stock uintptr, name string, flag int32, want int32) {
	t.Helper()
	rc, value := accessCall(t, stock, name, flag)
	if rc != sqlite3.SQLITE_OK || value != want {
		t.Fatalf("stock setup access %s %d: %d %d", name, flag, rc, value)
	}
	h, e := persistentOpen(a.root.h, filepath.Base(name), false)
	if e != nil {
		if want == 0 && errors.Is(e, windows.STATUS_OBJECT_NAME_NOT_FOUND) {
			return
		}
		t.Fatalf("native setup %v", e)
	}
	s, e := inspect(h)
	ce := windows.CloseHandle(h)
	if e != nil || ce != nil {
		t.Fatalf("native metadata %v %v", e, ce)
	}
	got := int32(1)
	if flag == sqlite3.SQLITE_ACCESS_EXISTS && s.Standard.Size == 0 {
		got = 0
	}
	if flag == sqlite3.SQLITE_ACCESS_READWRITE && s.Basic.Attributes&windows.FILE_ATTRIBUTE_READONLY != 0 {
		got = 0
	}
	if got != want {
		t.Fatalf("native control=%d want=%d", got, want)
	}
}
func accessRequire(t *testing.T, a *persistentAccessProbe, v uintptr, name string, flag, want int32) {
	t.Helper()
	before := sqlite3.ExperimentalAccessHits(v)
	rc, value := accessCall(t, v, name, flag)
	if sqlite3.ExperimentalAccessHits(v) != before+1 {
		t.Fatal("private callback not reached")
	}
	if rc != sqlite3.SQLITE_OK || value != want {
		if errors.Is(a.last, errPersistentAccessUnimplemented) {
			t.Fatalf("ACCESS_OBSERVE_EXPECTED_RED: real private xAccess returned %d: %v", rc, a.last)
		}
		t.Fatalf("private access %d/%d want %d", rc, value, want)
	}
}
func TestPersistentAccessExistingWALAndJournalTruth(t *testing.T) {
	name, a, v, stock := accessFixture(t, true)
	journal := name + "-journal"
	if e := os.WriteFile(journal, []byte("nonempty metadata journal"), 0600); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(name)
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range []string{name + "-wal", journal} {
		accessControl(t, a, stock, n, sqlite3.SQLITE_ACCESS_EXISTS, 1)
	}
	t.Log("stock SQL, stock access and native metadata controls qualified")
	for _, n := range []string{name + "-wal", journal} {
		accessRequire(t, a, v, n, sqlite3.SQLITE_ACCESS_EXISTS, 1)
	}
	after, e := os.ReadFile(name)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("main changed")
	}
}
func TestPersistentAccessMissingAndEmptySemantics(t *testing.T) {
	name, a, v, stock := accessFixture(t, false)
	for _, s := range []string{"-wal", "-journal"} {
		n := name + s
		accessControl(t, a, stock, n, sqlite3.SQLITE_ACCESS_EXISTS, 0)
		if e := os.WriteFile(n, nil, 0600); e != nil {
			t.Fatal(e)
		}
		accessControl(t, a, stock, n, sqlite3.SQLITE_ACCESS_EXISTS, 0)
		accessControl(t, a, stock, n, sqlite3.SQLITE_ACCESS_READ, 1)
	}
	t.Log("missing/empty stock and native controls qualified")
	for _, s := range []string{"-wal", "-journal"} {
		n := name + s
		accessRequire(t, a, v, n, sqlite3.SQLITE_ACCESS_EXISTS, 0)
		accessRequire(t, a, v, n, sqlite3.SQLITE_ACCESS_READ, 1)
		if e := os.Remove(n); e != nil {
			t.Fatal(e)
		}
		accessRequire(t, a, v, n, sqlite3.SQLITE_ACCESS_EXISTS, 0)
	}
	accessControl(t, a, stock, name, sqlite3.SQLITE_ACCESS_READWRITE, 1)
	accessRequire(t, a, v, name, sqlite3.SQLITE_ACCESS_READWRITE, 1)
	w, e := windows.UTF16PtrFromString(name)
	if e != nil {
		t.Fatal(e)
	}
	attrs, e := windows.GetFileAttributes(w)
	if e != nil {
		t.Fatal(e)
	}
	if e = windows.SetFileAttributes(w, attrs|windows.FILE_ATTRIBUTE_READONLY); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := windows.SetFileAttributes(w, attrs); e != nil {
			t.Error(e)
		}
	}()
	accessControl(t, a, stock, name, sqlite3.SQLITE_ACCESS_READWRITE, 0)
	accessRequire(t, a, v, name, sqlite3.SQLITE_ACCESS_READWRITE, 0)
	accessRequire(t, a, v, name, sqlite3.SQLITE_ACCESS_READ, 1)

}
func TestPersistentAccessRefusesUnqualifiedObservation(t *testing.T) {
	name, a, v, stock := accessFixture(t, false)
	accessControl(t, a, stock, name, sqlite3.SQLITE_ACCESS_READ, 1)
	accessRequire(t, a, v, name, sqlite3.SQLITE_ACCESS_READ, 1)
	for _, bad := range []string{name + ":ads", filepath.Join(filepath.Dir(name), "..", "foreign.db"), name + "-other"} {
		rc, out := accessCall(t, v, bad, sqlite3.SQLITE_ACCESS_EXISTS)
		if rc != sqlite3.SQLITE_IOERR_ACCESS || out != 0 {
			t.Fatalf("unsafe observation %s: %d/%d", bad, rc, out)
		}
	}
	// A real reparse object is not qualified even if its target is regular.
	link := name + "-journal"
	target := name + ".target"
	if e := os.WriteFile(target, []byte("target"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(target, link); e != nil {
		t.Fatalf("reparse fixture setup: %v", e)
	}
	rcLink, outLink := accessCall(t, v, link, sqlite3.SQLITE_ACCESS_EXISTS)
	if rcLink != sqlite3.SQLITE_IOERR_ACCESS || outLink != 0 {
		t.Fatalf("reparse accepted %d/%d", rcLink, outLink)
	}
	if e := os.Remove(link); e != nil {
		t.Fatal(e)
	}

	for _, flag := range []int32{-1, 3, 0x100} {
		rc, out := accessCall(t, v, name, flag)
		if rc != sqlite3.SQLITE_IOERR_ACCESS || out != 0 {
			t.Fatalf("unknown flag %d accepted", flag)
		}
	}
	for _, kind := range []string{"hardlink", "directory"} {
		if kind == "hardlink" {
			if e := os.Link(target, link); e != nil {
				t.Fatal(e)
			}
		} else {
			if e := os.Mkdir(link, 0700); e != nil {
				t.Fatal(e)
			}
		}
		rc, out := accessCall(t, v, link, sqlite3.SQLITE_ACCESS_EXISTS)
		if rc != sqlite3.SQLITE_IOERR_ACCESS || out != 0 {
			t.Fatalf("%s accepted", kind)
		}
		if e := os.Remove(link); e != nil {
			t.Fatal(e)
		}
	}
	// A differently cased actual basename is not the canonical bound object.
	wrong := name + "-JOURNAL"
	if e := os.WriteFile(wrong, []byte("case mismatch"), 0600); e != nil {
		t.Fatal(e)
	}
	rcCase, outCase := accessCall(t, v, wrong, sqlite3.SQLITE_ACCESS_EXISTS)
	if rcCase != sqlite3.SQLITE_IOERR_ACCESS || outCase != 0 {
		t.Fatal("nonexact name accepted")
	}
	if e := os.Remove(wrong); e != nil {
		t.Fatal(e)
	}
	fault := errors.New("injected metadata open failure")
	a.open = func(windows.Handle, string) (windows.Handle, error) { return 0, fault }
	rc, out := accessCall(t, v, name, sqlite3.SQLITE_ACCESS_EXISTS)
	if rc != sqlite3.SQLITE_IOERR_ACCESS || out != 0 || !errors.Is(a.last, fault) {
		t.Fatalf("unknown open mistaken absent: %d/%d %v", rc, out, a.last)
	}

	var returned windows.Handle
	closes := 0
	a.open = func(parent windows.Handle, n string) (windows.Handle, error) {
		h, e := persistentOpen(parent, n, false)
		if e != nil {
			return h, e
		}
		returned = h
		return h, fault
	}
	a.close = func(h windows.Handle) error {
		if h == returned {
			closes++
		}
		return windows.CloseHandle(h)
	}
	rc, out = accessCall(t, v, name, sqlite3.SQLITE_ACCESS_EXISTS)
	if rc != sqlite3.SQLITE_IOERR_ACCESS || out != 0 || !errors.Is(a.last, fault) || returned == 0 || closes != 1 {
		t.Fatalf("handle+error ownership %d/%d %v closes=%d", rc, out, a.last, closes)
	}
	a.close = nil
	a.open = nil
	accessRequire(t, a, v, name, sqlite3.SQLITE_ACCESS_READ, 1)
	a.open = func(windows.Handle, string) (windows.Handle, error) { return 0, windows.STATUS_OBJECT_PATH_NOT_FOUND }
	rc, out = accessCall(t, v, name, sqlite3.SQLITE_ACCESS_EXISTS)
	if rc != sqlite3.SQLITE_IOERR_ACCESS || out != 0 {
		t.Fatal("ancestor/path failure called absent")
	}
	a.open = nil
	cleanup := errors.New("metadata cleanup uncertain")
	a.close = func(h windows.Handle) error { return errors.Join(windows.CloseHandle(h), cleanup) }
	rc, out = accessCall(t, v, name, sqlite3.SQLITE_ACCESS_EXISTS)
	if rc != sqlite3.SQLITE_IOERR_ACCESS || out != 0 || !errors.Is(a.last, cleanup) {
		t.Fatalf("cleanup produced observation: %d/%d %v", rc, out, a.last)
	}

	calls := 0
	a.close = nil
	a.open = func(windows.Handle, string) (windows.Handle, error) {
		calls++
		return 0, windows.STATUS_OBJECT_NAME_NOT_FOUND
	}
	rc, out = accessCall(t, v, name, sqlite3.SQLITE_ACCESS_EXISTS)
	if rc != sqlite3.SQLITE_IOERR_ACCESS || out != 0 || calls != 0 || !errors.Is(a.last, cleanup) {
		t.Fatal("cleanup-poisoned observer reopened")
	}
	other, b, bv, bs := accessFixture(t, false)
	accessControl(t, b, bs, other, sqlite3.SQLITE_ACCESS_READ, 1)
	accessRequire(t, b, bv, other, sqlite3.SQLITE_ACCESS_READ, 1)
	b.afterLineage = func() error { return windows.STATUS_OBJECT_NAME_NOT_FOUND }
	rc, out = accessCall(t, bv, other+"-journal", sqlite3.SQLITE_ACCESS_EXISTS)
	if rc != sqlite3.SQLITE_IOERR_ACCESS || out != 0 {
		t.Fatal("ancestor name failure called leaf absence")
	}
	b.afterLineage = nil
	rc, out = accessCall(t, bv, other, sqlite3.SQLITE_ACCESS_READ)
	if rc != sqlite3.SQLITE_IOERR_ACCESS || out != 0 {
		t.Fatal("lineage poison cleared")
	}
	closed, c, cv, cs := accessFixture(t, false)
	accessControl(t, c, cs, closed, sqlite3.SQLITE_ACCESS_READ, 1)
	accessRequire(t, c, cv, closed, sqlite3.SQLITE_ACCESS_READ, 1)
	if e := c.root.Close(); e != nil {
		t.Fatal(e)
	}
	rc, out = accessCall(t, cv, closed, sqlite3.SQLITE_ACCESS_EXISTS)
	if rc != sqlite3.SQLITE_IOERR_ACCESS || out != 0 {
		t.Fatal("closed root admitted observation")
	}

	panicName, p, pv, ps := accessFixture(t, false)
	accessControl(t, p, ps, panicName, sqlite3.SQLITE_ACCESS_READ, 1)
	accessRequire(t, p, pv, panicName, sqlite3.SQLITE_ACCESS_READ, 1)
	panicCloses := 0
	p.close = func(h windows.Handle) error {
		panicCloses++
		if e := windows.CloseHandle(h); e != nil {
			return e
		}
		panic("close unknown outcome")
	}
	rc, out = accessCall(t, pv, panicName, sqlite3.SQLITE_ACCESS_READ)
	if rc != sqlite3.SQLITE_IOERR_ACCESS || out != 0 || p.terminal == nil || panicCloses != 1 {
		t.Fatal("cleanup panic not terminal")
	}
	p.close = nil
	rc, out = accessCall(t, pv, panicName, sqlite3.SQLITE_ACCESS_READ)
	if rc != sqlite3.SQLITE_IOERR_ACCESS || out != 0 || panicCloses != 1 {
		t.Fatal("cleanup panic authority revived")
	}

}
func TestPersistentAccessObservationGrantsNoOpenAuthority(t *testing.T) {
	name, a, v, stock := accessFixture(t, false)
	n := name + "-journal"
	if e := os.WriteFile(n, []byte("old journal"), 0600); e != nil {
		t.Fatal(e)
	}
	accessControl(t, a, stock, n, sqlite3.SQLITE_ACCESS_EXISTS, 1)
	accessRequire(t, a, v, n, sqlite3.SQLITE_ACCESS_EXISTS, 1)
	accessOpenRefused(t, v, n)
	h, e := persistentOpen(a.root.h, filepath.Base(n), false)
	if e != nil {
		t.Fatal(e)
	}
	old, e := inspect(h)
	ce := windows.CloseHandle(h)
	if e != nil || ce != nil {
		t.Fatalf("old qualify %v %v", e, ce)
	}
	if e = os.Rename(n, n+".retired"); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(n, []byte("replacement journal"), 0600); e != nil {
		t.Fatal(e)
	}
	h, e = persistentOpen(a.root.h, filepath.Base(n), false)
	if e != nil {
		t.Fatal(e)
	}
	now, e := inspect(h)
	ce = windows.CloseHandle(h)
	if e != nil || ce != nil {
		t.Fatalf("fresh qualify %v %v", e, ce)
	}
	if old.ID == now.ID {
		t.Fatal("replacement not independent identity")
	}
	accessRequire(t, a, v, n, sqlite3.SQLITE_ACCESS_EXISTS, 1)
	accessOpenRefused(t, v, n)
	// Independent metadata confirms the fixture really rebound identity; only
	// the actual private FxOpen refusals above prove no content-open authority.
}

func accessOpenRefused(t *testing.T, v uintptr, name string) {
	t.Helper()
	tls := libc.NewTLS()
	defer tls.Close()
	n, e := libc.CString(name)
	if e != nil {
		t.Fatal(e)
	}
	// Name and file storage stay alive if unexpected publication cannot be retired.
	retain := false
	defer func() {
		if !retain {
			libc.Xfree(tls, n)
		}
	}()
	p := (*sqlite3.Tsqlite3_vfs)(unsafe.Pointer(v))
	file := libc.Xcalloc(tls, 1, uint64(p.FszOsFile))
	if file == 0 {
		t.Fatal("file allocation")
	}
	defer func() {
		methods := (*sqlite3.Tsqlite3_file)(unsafe.Pointer(file)).FpMethods
		if methods != 0 {
			table := (*sqlite3.Tsqlite3_io_methods)(unsafe.Pointer(methods))
			if table.FxClose == 0 {
				retain = true
				t.Error("unexpected published file has no xClose; retaining live file/name allocation")
				return
			}
			// Consume exactly once even when xOpen unexpectedly published on failure.
			// Unknown close outcome is not permission to free or retry a live object.
			retain = true
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("unexpected published file xClose panic; allocations retained: %v", p)
				}
			}()
			closeFn := *(*func(*libc.TLS, uintptr) int32)(unsafe.Pointer(&table.FxClose))
			rc := closeFn(tls, file)
			if rc != sqlite3.SQLITE_OK {
				t.Errorf("unexpected published file xClose=%d; allocations retained", rc)
				return
			}
			retain = false
		}
		libc.Xfree(tls, file)
	}()
	out := int32(-1)
	fn := *(*func(*libc.TLS, uintptr, uintptr, uintptr, int32, uintptr) int32)(unsafe.Pointer(&p.FxOpen))
	rc := fn(tls, v, n, file, sqlite3.SQLITE_OPEN_READONLY|sqlite3.SQLITE_OPEN_MAIN_JOURNAL, uintptr(unsafe.Pointer(&out)))
	if rc != sqlite3.SQLITE_CANTOPEN || out != 0 || (*sqlite3.Tsqlite3_file)(unsafe.Pointer(file)).FpMethods != 0 {
		t.Fatalf("observation granted content open %d/%d", rc, out)
	}
}
