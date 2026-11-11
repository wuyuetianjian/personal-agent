//go:build windows && nativeprobe_sqlite_identity

package rootlease

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"unsafe"

	"agent/internal/nativefs/windowsprobe"
	"agent/internal/storage"
	"golang.org/x/sys/windows"
)

// This is a real main-file consumer in an experimental test binary, NOT complete
// DB exclusion: WAL/SHM/journals/attachments/private-directory binding are absent.
type experimentalMainHandle interface {
	ExperimentalDuplicateMainFileHandle() (uintptr, error)
}

func duplicateExperimentalMain(raw any) (windows.Handle, error) {
	provider, ok := raw.(experimentalMainHandle)
	if !ok || provider == nil {
		return 0, errors.New("experimental SQLite identity interface unavailable")
	}
	h, err := provider.ExperimentalDuplicateMainFileHandle()
	if err != nil {
		// Contract violations must not leak an unexpectedly returned duplicate.
		if h != 0 && windows.Handle(h) != windows.InvalidHandle {
			_ = windows.CloseHandle(windows.Handle(h))
		}
		return 0, err
	}
	if h == 0 || windows.Handle(h) == windows.InvalidHandle {
		return 0, errors.New("experimental SQLite identity returned invalid handle")
	}
	return windows.Handle(h), nil
}

type sqliteIdentityBackend struct {
	includeJournal bool
	root           *windowsprobe.Root
	db             *sql.DB
	acquisitions   atomic.Int32
	closes         atomic.Int32
}

func (b *sqliteIdentityBackend) read(ctx context.Context, components []string) (data []byte, err error) {
	connection, err := b.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := connection.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		if err != nil {
			data = nil
		}
	}()
	err = connection.Raw(func(raw any) (err error) {
		b.acquisitions.Add(1)
		if b.includeJournal {
			var handles []windows.Handle
			handles, err = duplicateExperimentalCurrentWAL(raw)
			if err != nil {
				return err
			}
			defer func() {
				if e := closeWALDuplicates(handles); e != nil {
					err = errors.Join(err, e)
				}
			}()
			data, err = b.root.ExperimentalReadExcluding(ctx, components, handles)
			return err
		}
		duplicate, err := duplicateExperimentalMain(raw)
		if err != nil {
			return err
		}
		defer func() {
			if closeErr := windows.CloseHandle(duplicate); closeErr != nil {
				err = errors.Join(err, closeErr)
			}
		}()
		// Raw owns the driver-connection window until the native read finishes.
		// Never cache IDs or reacquire the DB file by pathname.
		data, err = b.root.ExperimentalReadExcluding(ctx, components, []windows.Handle{duplicate})
		return err
	})
	if err != nil {
		data = nil
	}
	return data, err
}
func (b *sqliteIdentityBackend) close() error { b.closes.Add(1); return b.root.Close() }

func sqliteIdentityFixture(t *testing.T) (*storage.DB, *registry, binding, *sqliteIdentityBackend) {
	t.Helper()
	dir := nativeLongFixturePath(t)
	db, err := storage.OpenSQLite(context.Background(), filepath.Join(dir, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := db.SQL.Exec("CREATE TABLE identity_probe (value INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "neighbor.txt"), []byte("ordinary neighbor\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var native *sqliteIdentityBackend
	registry := fixtureRegistry(t, 1, func(ctx context.Context, path string) (backend, error) {
		root, err := windowsprobe.OpenLocalRoot(path)
		if err != nil {
			return nil, err
		}
		native = &sqliteIdentityBackend{root: root, db: db.SQL}
		return native, nil
	})
	p, err := registry.acquire(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	b := binding{reference: p.reference(), path: dir, scope: []byte("main-only experiment"), policy: []byte("not complete DB exclusion")}
	if err := p.activate(b); err != nil {
		t.Fatal(err)
	}
	return db, registry, b, native
}

func TestNativeSQLiteMainIdentityProtectedAndNeighbor(t *testing.T) {
	_, registry, b, native := sqliteIdentityFixture(t)
	data, err := registry.read(context.Background(), b, []string{"notes.txt"})
	// Sentinel identity through wrapping is required; generic refusals cannot pass.
	if !errors.Is(err, windowsprobe.ErrProtected) || data != nil {
		t.Fatalf("main DB must return ErrProtected before content I/O: data_len=%d error=%v", len(data), err)
	}
	data, err = registry.read(context.Background(), b, []string{"neighbor.txt"})
	if err != nil || string(data) != "ordinary neighbor\n" {
		t.Fatalf("neighbor data=%q error=%v", data, err)
	}
	if native.acquisitions.Load() != 2 {
		t.Fatalf("per-read acquisition count=%d", native.acquisitions.Load())
	}
}

func TestNativeSQLiteMainIdentityDuplicateCloseDoesNotCloseSQLite(t *testing.T) {
	db, _, _, _ := sqliteIdentityFixture(t)
	conn, err := db.SQL.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = conn.Raw(func(raw any) error {
		duplicate, err := duplicateExperimentalMain(raw)
		if err != nil {
			return err
		}
		// Metadata query proves this is a live OS handle, not just a nonzero value.
		var info windows.ByHandleFileInformation
		inspectErr := windows.GetFileInformationByHandle(duplicate, &info)
		var access uint32
		var io windows.IO_STATUS_BLOCK
		accessErr := windows.NtQueryInformationFile(duplicate, &io, (*byte)(unsafe.Pointer(&access)), uint32(unsafe.Sizeof(access)), 8 /* FileAccessInformation */)
		if accessErr == nil && (io.Status != 0 || io.Information != unsafe.Sizeof(access) || access != uint32(windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE)) {
			accessErr = fmt.Errorf("duplicate access contract: status=%x size=%d granted=%x", io.Status, io.Information, access)
		}
		var flags uint32
		var flagsErr error
		getFlags := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation")
		if flagsErr = getFlags.Find(); flagsErr == nil {
			ok, _, callErr := getFlags.Call(uintptr(duplicate), uintptr(unsafe.Pointer(&flags)))
			if ok == 0 {
				flagsErr = callErr
			}
		}
		if flagsErr == nil && flags&windows.HANDLE_FLAG_INHERIT != 0 {
			flagsErr = fmt.Errorf("duplicate handle unexpectedly inheritable")
		}
		if err := errors.Join(inspectErr, accessErr, flagsErr, windows.CloseHandle(duplicate)); err != nil {
			return err
		}
		// Reacquire from the same live SQLite connection after closing the first
		// duplicate. This fails if the driver accidentally returned its own handle.
		second, err := duplicateExperimentalMain(raw)
		if err != nil {
			return err
		}
		var secondInfo windows.ByHandleFileInformation
		return errors.Join(windows.GetFileInformationByHandle(second, &secondInfo), windows.CloseHandle(second))
	})
	defer conn.Close()
	if err != nil {
		t.Fatalf("duplicate acquisition/close: %v", err)
	}
	// Keep the exact driver connection borrowed: DB.Exec could silently open a
	// new connection and conceal accidentally closing SQLite's original handle.
	if _, err := conn.ExecContext(context.Background(), "INSERT INTO identity_probe VALUES (1)"); err != nil {
		t.Fatalf("SQLite original handle damaged: %v", err)
	}
	var busy, logFrames, checkpointed int
	if err := conn.QueryRowContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed); err != nil || busy != 0 || logFrames != 0 || checkpointed != 0 {
		t.Fatalf("same-connection checkpoint: busy=%d log=%d checkpointed=%d error=%v", busy, logFrames, checkpointed, err)
	}
	var count int
	if err := conn.QueryRowContext(context.Background(), "SELECT count(*) FROM identity_probe").Scan(&count); err != nil || count != 1 {
		t.Fatalf("SQLite same-connection continued use count=%d error=%v", count, err)
	}
}

func TestNativeSQLiteMainIdentityUnavailableInputs(t *testing.T) {
	for _, raw := range []any{nil, struct{}{}} {
		if h, err := duplicateExperimentalMain(raw); err == nil || h != 0 {
			t.Fatal("missing interface accepted")
		}
	}
	db, err := storage.OpenSQLite(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.SQL.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = conn.Raw(func(raw any) error {
		// Absence of the experimental patch is a setup failure, not memory proof.
		if _, ok := raw.(experimentalMainHandle); !ok {
			return fmt.Errorf("required experimental driver patch absent")
		}
		h, rejection := duplicateExperimentalMain(raw)
		if rejection == nil || h != 0 {
			if h != 0 {
				windows.CloseHandle(h)
			}
			return fmt.Errorf("in-memory DB accepted")
		}
		return nil
	})
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	called := false
	err = conn.Raw(func(any) error { called = true; return nil })
	if !errors.Is(err, sql.ErrConnDone) || called {
		t.Fatalf("closed connection invoked raw callback: called=%v error=%v", called, err)
	}
}

func TestNativeSQLiteMainIdentityConnectionRecreation(t *testing.T) {
	db, registry, b, native := sqliteIdentityFixture(t)
	db.SQL.SetMaxIdleConns(0) // Every returned connection closes; no cached identity.
	var first any
	conn, err := db.SQL.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Raw(func(raw any) error { first = raw; return nil }); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		data, err := registry.read(context.Background(), b, []string{"neighbor.txt"})
		if err != nil || string(data) != "ordinary neighbor\n" {
			t.Fatalf("recreated connection read %d: %q %v", i, data, err)
		}
	}
	if native.acquisitions.Load() != 2 {
		t.Fatalf("new acquisitions=%d", native.acquisitions.Load())
	}
	conn, err = db.SQL.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Raw(func(raw any) error {
		if raw == first {
			return fmt.Errorf("driver connection not recreated")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeSQLiteMainIdentityCloseLifetimes(t *testing.T) {
	t.Run("db-first", func(t *testing.T) {
		db, registry, b, native := sqliteIdentityFixture(t)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		data, err := registry.read(context.Background(), b, []string{"neighbor.txt"})
		if err == nil || data != nil || native.acquisitions.Load() != 0 {
			t.Fatalf("closed DB read: %q %v", data, err)
		}
		if err := registry.close(); err != nil {
			t.Fatal(err)
		}
		if native.closes.Load() != 1 {
			t.Fatal("backend not closed once")
		}
	})
	t.Run("lease-first", func(t *testing.T) {
		db, registry, b, native := sqliteIdentityFixture(t)
		if err := registry.close(); err != nil {
			t.Fatal(err)
		}
		data, err := registry.read(context.Background(), b, []string{"neighbor.txt"})
		if err == nil || data != nil || native.acquisitions.Load() != 0 {
			t.Fatalf("closed lease read: %q %v", data, err)
		}
		if _, err := db.SQL.Exec("INSERT INTO identity_probe VALUES (2)"); err != nil {
			t.Fatalf("lease close damaged DB: %v", err)
		}
	})
}
