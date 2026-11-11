//go:build windows && nativeprobe_sqlite_identity

package rootlease

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"unsafe"

	"agent/internal/nativefs/windowsprobe"
	"golang.org/x/sys/windows"
)

// Positive delivery contract: refusing xOpen must fail real SQLite spill, not skip.
func TestNativeSQLiteTempSpillUsesOwnedScratch(t *testing.T) {
	dir, err := os.MkdirTemp("", "agent-temp-vfs-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := os.Remove(dir); e != nil {
			t.Error(e)
		}
	}()
	// Host-created fixture directory is the trusted initial authority, not a
	// production pathname acquisition or user-controlled VFS filename.
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	captureHandleInventory(t)
	baseline := captureHandleInventory(t)
	observed, e := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		h := observed
		observed = 0
		if h != 0 {
			if e := windows.CloseHandle(h); e != nil {
				t.Error(e)
			}
		}
	}()
	live := captureHandleInventory(t)
	controlOK := !baseline.files[observed] && live.files[observed]
	ownedObservation := observed
	observed = 0
	if e := windows.CloseHandle(ownedObservation); e != nil {
		t.Fatal(e)
	}
	if !controlOK || !sameDiskHandles(baseline, captureHandleInventory(t)) {
		t.Fatal("TEMP PSS positive control failed")
	}
	h, err := windows.CreateFile(p, windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	var rootID struct {
		Volume uint64
		ID     [16]byte
	}
	idErr := windows.GetFileInformationByHandleEx(h, windows.FileIdInfo, (*byte)(unsafe.Pointer(&rootID)), uint32(unsafe.Sizeof(rootID)))
	root, rootErr := windowsprobe.NewRoot(h)
	closeErr := windows.CloseHandle(h)
	if root != nil {
		defer func() {
			if e := root.Close(); e != nil {
				t.Error(e)
			}
		}()
	}
	if e := errors.Join(idErr, rootErr, closeErr); e != nil {
		t.Fatal(e)
	}
	owner, err := windowsprobe.NewExperimentalTempVFSWithScratch(root)
	if owner != nil {
		defer func() {
			if e := owner.Close(); e != nil {
				t.Error(e)
			}
		}()
	}
	if err != nil {
		t.Fatal(err)
	}
	if e := root.Close(); e != nil {
		t.Fatal(e)
	}
	err = owner.WithConnection(context.Background(), func(conn *sql.Conn) error {
		for _, q := range []string{"PRAGMA temp_store=FILE", "PRAGMA temp.page_size=4096", "PRAGMA temp.cache_size=8", "PRAGMA cache_spill=ON"} {
			if _, err := conn.ExecContext(context.Background(), q); err != nil {
				return err
			}
		}
		var mode string
		if err := conn.QueryRowContext(context.Background(), "PRAGMA temp.journal_mode=MEMORY").Scan(&mode); err != nil {
			return err
		}
		if !strings.EqualFold(mode, "memory") {
			return fmt.Errorf("temp journal mode=%q", mode)
		}
		for q, want := range map[string]int{"PRAGMA temp_store": 1, "PRAGMA temp.page_size": 4096, "PRAGMA temp.cache_size": 8} {
			var got int
			if err := conn.QueryRowContext(context.Background(), q).Scan(&got); err != nil {
				return err
			}
			if got != want {
				return fmt.Errorf("%s=%d want %d", q, got, want)
			}
		}
		var spillThreshold int
		if err := conn.QueryRowContext(context.Background(), "PRAGMA temp.cache_spill").Scan(&spillThreshold); err != nil {
			return err
		}
		if spillThreshold <= 0 {
			return fmt.Errorf("cache spill disabled: threshold=%d", spillThreshold)
		}
		if _, err := conn.ExecContext(context.Background(), "CREATE TEMP TABLE spill(id INTEGER PRIMARY KEY, payload BLOB NOT NULL)"); err != nil {
			return err
		}
		tx, err := conn.BeginTx(context.Background(), nil)
		if err != nil {
			return err
		}
		defer func() {
			if e := tx.Rollback(); e != nil && !errors.Is(e, sql.ErrTxDone) {
				t.Errorf("rollback cleanup: %v", e)
			}
		}()
		stmt, err := tx.PrepareContext(context.Background(), "INSERT INTO spill VALUES (?,?)")
		if err != nil {
			return err
		}
		payloadFor := func(id int) []byte {
			b := bytes.Repeat([]byte{0x53}, 8192)
			binary.LittleEndian.PutUint64(b[:8], uint64(id))
			return b
		}
		for i := 0; i < 1024; i++ {
			if _, err = stmt.ExecContext(context.Background(), i, payloadFor(i)); err != nil {
				return fmt.Errorf("insert %d: %w", i, errorsJoinClose(stmt, err))
			}
		}
		if err := stmt.Close(); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		var count, sum, byteCount int64
		if err := conn.QueryRowContext(context.Background(), "SELECT count(*),sum(id),sum(length(payload)) FROM spill").Scan(&count, &sum, &byteCount); err != nil {
			return err
		}
		if count != 1024 || sum != 1023*1024/2 || byteCount != int64(1024*8192) {
			return fmt.Errorf("SQL checksum count=%d sum=%d bytes=%d", count, sum, byteCount)
		}
		readsBeforeScans := owner.Stats().ReadBytes
		for _, descending := range []bool{false, true} {
			direction := "ASC"
			if descending {
				direction = "DESC"
			}
			if err := func() (result error) {
				rows, e := conn.QueryContext(context.Background(), "SELECT id,payload FROM spill ORDER BY id "+direction)
				if e != nil {
					return e
				}
				defer func() { result = errors.Join(result, rows.Close()) }()
				actual, wantHash := sha256.New(), sha256.New()
				index := 0
				for rows.Next() {
					var id int
					var payload []byte
					if e := rows.Scan(&id, &payload); e != nil {
						return e
					}
					want := index
					if descending {
						want = 1023 - index
					}
					if index >= 1024 || id != want || !bytes.Equal(payload, payloadFor(want)) {
						return fmt.Errorf("host row mismatch direction=%s index=%d id=%d", direction, index, id)
					}
					actual.Write(payload)
					wantHash.Write(payloadFor(want))
					index++
				}
				if e := rows.Err(); e != nil {
					return e
				}
				if index != 1024 || !bytes.Equal(actual.Sum(nil), wantHash.Sum(nil)) {
					return fmt.Errorf("host SHA/count mismatch %s count=%d", direction, index)
				}
				t.Logf("host verified order=%s rows=%d sha256=%x", direction, index, actual.Sum(nil))
				return nil
			}(); err != nil {
				return err
			}
		}
		if owner.Stats().ReadBytes <= readsBeforeScans {
			return errors.New("host SHA scans did not perform actual kernel reads")
		}
		observed, observeErr := owner.Evidence()
		if observeErr != nil {
			return observeErr
		}
		if len(observed) == 0 {
			return errors.New("no live qualified TEMP metadata")
		}
		for _, item := range observed {
			if item.Volume != rootID.Volume || item.ID == [16]byte{} || item.Size <= 0 {
				return fmt.Errorf("invalid live TEMP metadata: %+v", item)
			}
		}

		return nil
	})
	stats := owner.Stats()
	t.Logf("TEMP VFS evidence: %+v last_flags=0x%x anonymous_exact_temp_attempts=%d", stats, stats.LastFlags, stats.TempAttempts)
	if stats.TempAttempts == 0 {
		t.Fatalf("workload did not demonstrate approved anonymous TEMP_DB open: SQL=%v stats=%+v", err, stats)
	}
	if err != nil {
		t.Fatalf("real TEMP spill refused; owned scratch not implemented: %v", err)
	}
	if stats.Opened == 0 || stats.WrittenBytes == 0 || stats.ReadBytes == 0 {
		t.Fatal("SQL completed without actual owned scratch disk evidence")
	}
	if stats.PeakKernelSize == 0 || stats.LastFileID == [16]byte{} || stats.LastVolume == 0 {
		t.Fatalf("missing actual kernel size/identity: %+v", stats)
	}
	if stats.ClosedSuccessfully != stats.Opened || stats.LiveFiles != 0 || stats.InflightCallbacks != 0 {
		t.Fatalf("TEMP file/callback drain incomplete: %+v", stats)
	}
	if e := owner.Close(); e != nil {
		t.Fatal(e)
	}
	terminal := owner.Stats()
	if terminal.RegistrationAlive || terminal.MethodsAlive || terminal.PhysicalConnections != 0 || terminal.ConnectionLeases != 0 || terminal.LiveFiles != 0 || terminal.InflightCallbacks != 0 {
		t.Fatalf("TEMP lifecycle retained resources: %+v", terminal)
	}

	if entries, e := os.ReadDir(dir); e != nil || len(entries) != 0 {
		t.Fatalf("TEMP objects remain: %v %v", entries, e)
	}
	if after := captureHandleInventory(t); !sameDiskHandles(baseline, after) {
		t.Fatalf("TEMP disk handle leak: before=%v after=%v", baseline.files, after.files)
	}

}
func errorsJoinClose(stmt *sql.Stmt, prior error) error {
	if err := stmt.Close(); err != nil {
		return fmt.Errorf("%w; statement close: %v", prior, err)
	}
	return prior
}
