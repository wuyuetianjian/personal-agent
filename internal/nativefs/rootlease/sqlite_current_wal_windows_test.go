//go:build windows && nativeprobe_sqlite_identity

package rootlease

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"unsafe"

	"agent/internal/nativefs/windowsprobe"
	"golang.org/x/sys/windows"
)

// Qualification uses the controlled experimental private-cache driver with
// absolute file fixtures, not production storage.Open. No user URI/shared-cache
// or parallel SQLite extensions are qualified; Raw alone does not freeze pagers.
type experimentalJournalHandles interface {
	ExperimentalDuplicateMainAndJournalFileHandles() ([]uintptr, error)
}

func closeWALDuplicates(handles []windows.Handle) (err error) {
	for _, h := range handles {
		if h != 0 && h != windows.InvalidHandle {
			err = errors.Join(err, windows.CloseHandle(h))
		}
	}
	return err
}
func duplicateExperimentalCurrentWAL(raw any) ([]windows.Handle, error) {
	provider, ok := raw.(experimentalJournalHandles)
	if !ok {
		return nil, errors.New("experimental current-journal interface unavailable")
	}
	rawHandles, err := provider.ExperimentalDuplicateMainAndJournalFileHandles()
	handles := make([]windows.Handle, len(rawHandles))
	for i, h := range rawHandles {
		handles[i] = windows.Handle(h)
	}
	if err != nil {
		return nil, errors.Join(err, closeWALDuplicates(handles))
	}
	if len(handles) != 2 || handles[0] == 0 || handles[1] == 0 || handles[0] == windows.InvalidHandle || handles[1] == windows.InvalidHandle || handles[0] == handles[1] {
		return nil, errors.Join(errors.New("expected two independent main/current-WAL duplicates"), closeWALDuplicates(handles))
	}
	return handles, nil
}

type walFileID struct {
	Volume uint64
	File   [16]byte
}

func walIdentity(h windows.Handle) (walFileID, error) {
	var id walFileID
	err := windows.GetFileInformationByHandleEx(h, windows.FileIdInfo, (*byte)(unsafe.Pointer(&id)), uint32(unsafe.Sizeof(id)))
	return id, err
}

// Independent fixture observation ONLY; never used as the protection input.
func fixtureWALIdentity(path string) (walFileID, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return walFileID{}, err
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return walFileID{}, err
	}
	id, err := walIdentity(h)
	return id, errors.Join(err, windows.CloseHandle(h))
}
func checkWALDuplicateRights(h windows.Handle) error {
	var access uint32
	var io windows.IO_STATUS_BLOCK
	if err := windows.NtQueryInformationFile(h, &io, (*byte)(unsafe.Pointer(&access)), 4, 8); err != nil {
		return err
	}
	if io.Status != 0 || io.Information != 4 || access != windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE {
		return fmt.Errorf("duplicate access: status=%x size=%d access=%x", io.Status, io.Information, access)
	}
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation")
	if err := proc.Find(); err != nil {
		return err
	}
	var flags uint32
	ok, _, err := proc.Call(uintptr(h), uintptr(unsafe.Pointer(&flags)))
	if ok == 0 {
		return err
	}
	if flags&windows.HANDLE_FLAG_INHERIT != 0 {
		return errors.New("inheritable duplicate")
	}
	return nil
}
func currentWALCheckpoint(conn *sql.Conn) error {
	var busy, log, done int
	err := conn.QueryRowContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &done)
	if err != nil {
		return err
	}
	if busy != 0 || log != 0 || done != 0 {
		return fmt.Errorf("checkpoint busy=%d log=%d done=%d", busy, log, done)
	}
	return nil
}
func currentWALCollect(conn *sql.Conn, inspect func([]windows.Handle) error) error {
	return conn.Raw(func(raw any) (err error) {
		handles, err := duplicateExperimentalCurrentWAL(raw)
		if err != nil {
			return err
		}
		defer func() {
			if e := closeWALDuplicates(handles); e != nil {
				err = errors.Join(err, e)
			}
		}()
		return inspect(handles)
	})
}

func TestNativeSQLiteCurrentWALProtectedAndIdentities(t *testing.T) {
	db, registry, b, native := privateWALFixture(t)
	native.includeJournal = true
	// Committed disk-backed WAL exists before entering Raw; no SQL inside Raw.
	if _, err := db.SQL.Exec("INSERT INTO identity_probe VALUES (10)"); err != nil {
		t.Fatal(err)
	}
	mainID, err := fixtureWALIdentity(filepath.Join(b.path, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	walID, err := fixtureWALIdentity(filepath.Join(b.path, "notes.txt-wal"))
	if err != nil {
		t.Fatal(err)
	}
	if mainID == walID {
		t.Fatal("fixture main and WAL share identity")
	}
	conn, err := db.SQL.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = currentWALCollect(conn, func(handles []windows.Handle) error {
		main, e1 := walIdentity(handles[0])
		wal, e2 := walIdentity(handles[1])
		if e := errors.Join(e1, e2); e != nil {
			return e
		}
		if main != mainID || wal != walID || main == wal {
			return errors.New("actual SQLite handles mismatch independent fixture identities")
		}
		return nil
	})
	closeErr := conn.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("current WAL identity: %v / %v", err, closeErr)
	}
	for _, name := range []string{"notes.txt", "notes.txt-wal"} {
		data, err := registry.read(context.Background(), b, []string{name})
		if !errors.Is(err, windowsprobe.ErrProtected) || data != nil {
			t.Fatalf("%s must return ErrProtected: length=%d error=%v", name, len(data), err)
		}
	}
	data, err := registry.read(context.Background(), b, []string{"neighbor.txt"})
	if err != nil || string(data) != "ordinary neighbor\n" {
		t.Fatalf("neighbor: %q %v", data, err)
	}
	if native.acquisitions.Load() != 3 {
		t.Fatalf("acquisitions=%d", native.acquisitions.Load())
	}
}

func TestNativeSQLiteCurrentWALDuplicateRightsAndOwnership(t *testing.T) {
	db, _, _, _ := privateWALFixture(t)
	conn, err := db.SQL.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "INSERT INTO identity_probe VALUES (11)"); err != nil {
		t.Fatal(err)
	}
	// Two complete collections in the SAME Raw window. No SQL or goroutines.
	err = conn.Raw(func(raw any) error {
		for i := 0; i < 2; i++ {
			handles, err := duplicateExperimentalCurrentWAL(raw)
			if err != nil {
				return err
			}
			var checked error
			for _, h := range handles {
				_, e := walIdentity(h)
				checked = errors.Join(checked, e, checkWALDuplicateRights(h))
			}
			if err := errors.Join(checked, closeWALDuplicates(handles)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), "INSERT INTO identity_probe VALUES (12)"); err != nil {
		t.Fatal(err)
	}
	if err := currentWALCheckpoint(conn); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := conn.QueryRowContext(context.Background(), "SELECT count(*) FROM identity_probe").Scan(&count); err != nil || count != 2 {
		t.Fatalf("source survival count=%d err=%v", count, err)
	}
}

func TestNativeSQLiteCurrentWALMissingJournalRefusesWithoutLeak(t *testing.T) {
	db, _, _, _ := privateWALFixture(t)
	conn, err := db.SQL.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := currentWALCheckpoint(conn); err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := conn.QueryRowContext(context.Background(), "PRAGMA journal_mode=DELETE").Scan(&mode); err != nil || mode != "delete" {
		t.Fatalf("mode=%s error=%v", mode, err)
	}
	err = conn.Raw(func(raw any) error {
		provider, ok := raw.(experimentalJournalHandles)
		if !ok {
			return errors.New("required journal patch absent")
		}
		refuse := func() error {
			hs, e := provider.ExperimentalDuplicateMainAndJournalFileHandles()
			if e == nil || len(hs) != 0 {
				for _, h := range hs {
					if h != 0 && windows.Handle(h) != windows.InvalidHandle {
						windows.CloseHandle(windows.Handle(h))
					}
				}
				return fmt.Errorf("missing journal returned handles=%d error=%v", len(hs), e)
			}
			return nil
		}
		// Warm lazy APIs; compare exact persistent Disk-handle sets, not total
		// process counts affected by Go thread/Event/Timer creation.
		for i := 0; i < 4; i++ {
			if e := refuse(); e != nil {
				return e
			}
		}
		captureHandleInventory(t)
		before := captureHandleInventory(t)
		for i := 0; i < 128; i++ {
			if e := refuse(); e != nil {
				return e
			}
		}
		after := captureHandleInventory(t)
		if !sameDiskHandles(before, after) {
			return fmt.Errorf("missing journal Disk handles differ: before=%v after=%v", before.files, after.files)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), "INSERT INTO identity_probe VALUES (13)"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := conn.QueryRowContext(context.Background(), "SELECT count(*) FROM identity_probe").Scan(&count); err != nil || count != 1 {
		t.Fatalf("source survival count=%d error=%v", count, err)
	}
}

func TestNativeSQLiteCurrentWALCheckpointAndConnectionRecreation(t *testing.T) {
	db, registry, b, native := privateWALFixture(t)
	native.includeJournal = true
	conn, err := db.SQL.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), "INSERT INTO identity_probe VALUES (14)"); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if err := currentWALCheckpoint(conn); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	walID, err := fixtureWALIdentity(filepath.Join(b.path, "notes.txt-wal"))
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	err = currentWALCollect(conn, func(hs []windows.Handle) error {
		id, e := walIdentity(hs[1])
		if e != nil {
			return e
		}
		if id != walID {
			return errors.New("post-checkpoint current WAL identity mismatch")
		}
		return nil
	})
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	db.SQL.SetMaxIdleConns(0)
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	// Open a fresh connection without a schema query/write to force a journal.
	// If SQLite legitimately opened one, use DELETE to establish absence; never
	// claim an unopened journal merely from connection recreation.
	conn, err = db.SQL.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := conn.QueryRowContext(context.Background(), "PRAGMA journal_mode=DELETE").Scan(&mode); err != nil || mode != "delete" {
		conn.Close()
		t.Fatalf("recreated DELETE mode=%s error=%v", mode, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := registry.read(context.Background(), b, []string{"neighbor.txt"})
	if err == nil || data != nil {
		t.Fatalf("missing current journal degraded to main-only: %q %v", data, err)
	}
}
