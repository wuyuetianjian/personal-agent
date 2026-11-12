//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_lease && nativeprobe_sqlite_permit && nativeprobe_sqlite_persistent && nativeprobe_sqlite_main_read && nativeprobe_sqlite_existing_wal && nativeprobe_sqlite_access && nativeprobe_sqlite_private_shm

package windowsprobe

import (
	"bufio"
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
	"os"
	"path/filepath"
	"testing"
)

// Same fixture routing as existingWALFixture, but this NEW stage's child holds an
// actual read transaction/snapshot, not just an idle open database connection.
func privateSQLAdmissionChild(t *testing.T) bool {
	path := os.Getenv(existingWALChildEnv)
	if path == "" {
		return false
	}
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	defer func() {
		if e := db.Close(); e != nil {
			t.Error(e)
		}
	}()
	var mode string
	if e = db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode); e != nil || mode != "wal" {
		t.Fatalf("fixture WAL %s %v", mode, e)
	}
	for _, q := range []string{"PRAGMA wal_autocheckpoint=0", "CREATE TABLE fixture(value TEXT)"} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	var busy, log, checkpointed int
	if e = db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpointed); e != nil || busy != 0 || log != 0 || checkpointed != 0 {
		t.Fatalf("baseline %d/%d/%d %v", busy, log, checkpointed, e)
	}
	if _, e = db.Exec("INSERT INTO fixture VALUES('committed-wal-only')"); e != nil {
		t.Fatal(e)
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := tx.Rollback(); e != nil {
			t.Error(e)
		}
	}()
	var value string
	if e = tx.QueryRow("SELECT value FROM fixture").Scan(&value); e != nil || value != "committed-wal-only" {
		t.Fatalf("held snapshot %q %v", value, e)
	}
	if _, e = fmt.Println("EXISTING_WAL_READY"); e != nil {
		t.Fatal(e)
	}
	scan := bufio.NewScanner(os.Stdin)
	if !scan.Scan() || scan.Text() != "release" {
		t.Fatalf("fixture release %v", scan.Err())
	}
	return true
}

type privateSQLAdmissionAuthority struct {
	*existingWALAuthority
	metadata *persistentAccessProbe
	observed []string
}

func (a *privateSQLAdmissionAuthority) Observe(name string, flags int32) (bool, error) {
	a.observed = append(a.observed, name)
	yes, e := a.metadata.Observe(name, flags)
	if e == nil && flags == sqlite3.SQLITE_ACCESS_READWRITE {
		return false, nil
	}
	return yes, e
}
func privateSQLAdmissionSetup(t *testing.T) (string, *privateSQLAdmissionAuthority, uintptr, string) {
	t.Helper()
	name, base, _ := existingWALSetup(t)
	metadata, e := newPersistentAccessProbe(base.root, name)
	if e != nil {
		t.Fatalf("metadata setup %v", e)
	}
	a := &privateSQLAdmissionAuthority{existingWALAuthority: base, metadata: metadata}
	v, vfs, e := sqlite3.ExperimentalSQLAdmissionRegisterRO(a, name)
	if e != nil {
		t.Fatalf("admission registration setup %v", e)
	}
	if yes, e := a.metadata.Observe(name+"-wal", sqlite3.SQLITE_ACCESS_EXISTS); e != nil || !yes {
		t.Fatalf("same-owner WAL metadata setup %t %v", yes, e)
	}
	return name, a, v, vfs
}
func privateSQLAdmissionQuery(uri string) (queryErr, closeErr error) {
	db, e := sql.Open("sqlite", uri)
	if e != nil {
		return e, nil
	}
	db.SetMaxOpenConns(1)
	var value string
	e = db.QueryRow("SELECT value FROM fixture").Scan(&value)
	return e, db.Close()
}
func privateSQLRequireSHM(t *testing.T, name string, a *privateSQLAdmissionAuthority, v uintptr, vfs string) {
	t.Helper()
	tls := libc.NewTLS()
	defer tls.Close()
	stock := sqlite3.Xsqlite3_vfs_find(tls, 0)
	before := sqlite3.ExperimentalSQLAdmissionCounters(v)
	e, closeErr := privateSQLAdmissionQuery(existingWALURI(name, "ro", vfs))
	if closeErr != nil {
		t.Fatalf("private SQL close: %v", closeErr)
	}
	existingWALStockRead(t, name)
	now := sqlite3.ExperimentalSQLAdmissionCounters(v)
	if stock == 0 || stock != sqlite3.Xsqlite3_vfs_find(tls, 0) {
		t.Fatal("default VFS changed")
	}
	if now.MainURI <= before.MainURI {
		t.Fatalf("real URI main callback not reached: %+v %v", now, e)
	}
	if now.Unimplemented > before.Unimplemented && e != nil {
		t.Fatalf("ADMISSION_MAIN_EXPECTED_RED: real private main0x141 admission unimplemented: %v", e)
	}
	if now.Access <= before.Access || now.WAL <= before.WAL || now.SHM <= before.SHM || len(a.observed) == 0 {
		t.Fatalf("SQL did not reach same-binding access/WAL/SHM: %+v %v", now, e)
	}
	var code interface{ Code() int }
	if e == nil || !errors.As(e, &code) || code.Code() != int(sqlite3.SQLITE_IOERR_SHMMAP) {
		t.Fatalf("expected explicit SHM IOERR, not success/heap fallback: %v", e)
	}
}
func TestPrivateSQLAdmissionReachesSHMRefusal(t *testing.T) {
	if privateSQLAdmissionChild(t) {
		return
	}
	name, a, v, vfs := privateSQLAdmissionSetup(t)
	privateSQLRequireSHM(t, name, a, v, vfs)
}
func TestPrivateSQLAdmissionRejectsURIEscapes(t *testing.T) {
	if privateSQLAdmissionChild(t) {
		return
	}
	name, a, v, vfs := privateSQLAdmissionSetup(t)
	privateSQLRequireSHM(t, name, a, v, vfs)
	base := existingWALURI(name, "ro", vfs)
	for _, uri := range []string{base + "&immutable=1", base + "&nolock=1", base + "&mode=ro", base + "&unknown=1", existingWALURI(name, "rw", vfs), existingWALURI(filepath.Join(filepath.Dir(name), "foreign.db"), "ro", vfs)} {
		before := sqlite3.ExperimentalSQLAdmissionCounters(v)
		e, closeErr := privateSQLAdmissionQuery(uri)
		if closeErr != nil {
			t.Fatalf("rejected URI close: %v", closeErr)
		}
		if e == nil {
			t.Fatalf("unsafe URI succeeded: %s", uri)
		}
		after := sqlite3.ExperimentalSQLAdmissionCounters(v)
		if after.WAL != before.WAL || after.SHM != before.SHM {
			t.Fatalf("unsafe URI reached WAL/SHM: %s", uri)
		}
	}
	existingWALStockRead(t, name)
}
func TestPrivateSQLAdmissionJournalTruth(t *testing.T) {
	if privateSQLAdmissionChild(t) {
		return
	}
	name, a, v, vfs := privateSQLAdmissionSetup(t)
	privateSQLRequireSHM(t, name, a, v, vfs)
	journal := name + "-journal"
	rc, value := accessCall(t, v, journal, sqlite3.SQLITE_ACCESS_EXISTS)
	if rc != sqlite3.SQLITE_OK || value != 0 {
		t.Fatalf("missing journal not qualified absent %d/%d", rc, value)
	}
	data := make([]byte, 512)
	copy(data, []byte{0xd9, 0xd5, 0x05, 0xf9, 0x20, 0xa1, 0x63, 0xd7})
	if e := os.WriteFile(journal, data, 0600); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := os.Remove(journal); e != nil {
			t.Error(e)
		}
	}()
	rc, value = accessCall(t, v, journal, sqlite3.SQLITE_ACCESS_EXISTS)
	if rc != sqlite3.SQLITE_OK || value != 1 {
		t.Fatalf("present journal hidden %d/%d", rc, value)
	}
	accessOpenRefused(t, v, journal)
	before := sqlite3.ExperimentalSQLAdmissionCounters(v)
	observedBefore := len(a.observed)
	e, closeErr := privateSQLAdmissionQuery(existingWALURI(name, "ro", vfs))
	if closeErr != nil {
		t.Fatalf("private SQL close: %v", closeErr)
	}
	if e == nil {
		t.Fatal("unsupported journal/recovery SQL succeeded")
	}
	afterCounters := sqlite3.ExperimentalSQLAdmissionCounters(v)
	sawJournal := false
	for _, n := range a.observed[observedBefore:] {
		if n == journal {
			sawJournal = true
		}
	}
	if !sawJournal || afterCounters.JournalRejected <= before.JournalRejected {
		t.Fatal("unsupported journal callback not reached by SQL")
	}
	if afterCounters.SHM != before.SHM {
		t.Fatal("present journal path was hidden until SHM")
	}
	var code interface{ Code() int }
	if errors.As(e, &code) && code.Code() == int(sqlite3.SQLITE_IOERR_SHMMAP) {
		t.Fatal("journal rejection was only unrelated SHM refusal")
	}

	after, e := os.ReadFile(journal)
	if e != nil || !bytes.Equal(after, data) {
		t.Fatalf("journal mutated %v", e)
	}
}
