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
	"strings"
	"testing"

	"agent/internal/nativefs/windowsprobe"
)

// Positive delivery contract: refusing xOpen must fail real SQLite spill, not skip.
func TestNativeSQLiteTempSpillUsesOwnedScratch(t *testing.T) {
	owner, err := windowsprobe.NewExperimentalTempVFS()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	}()
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
}
func errorsJoinClose(stmt *sql.Stmt, prior error) error {
	if err := stmt.Close(); err != nil {
		return fmt.Errorf("%w; statement close: %v", prior, err)
	}
	return prior
}
