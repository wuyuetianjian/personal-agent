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
	a := &persistentAccessProbe{root: root, canonical: name}
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
	fault := errors.New("injected metadata open failure")
	a.open = func(windows.Handle, string) (windows.Handle, error) { return 0, fault }
	rc, out := accessCall(t, v, name, sqlite3.SQLITE_ACCESS_EXISTS)
	if rc != sqlite3.SQLITE_IOERR_ACCESS || out != 0 || !errors.Is(a.last, fault) {
		t.Fatalf("unknown open mistaken absent: %d/%d %v", rc, out, a.last)
	}
	a.open = nil
	cleanup := errors.New("metadata cleanup uncertain")
	a.close = func(h windows.Handle) error { return errors.Join(windows.CloseHandle(h), cleanup) }
	rc, out = accessCall(t, v, name, sqlite3.SQLITE_ACCESS_EXISTS)
	if rc != sqlite3.SQLITE_IOERR_ACCESS || out != 0 || !errors.Is(a.last, cleanup) {
		t.Fatalf("cleanup produced observation: %d/%d %v", rc, out, a.last)
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
