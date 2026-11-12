//go:build windows && nativeprobe_sqlite_persistent

package rootlease

import (
	"bufio"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// This process never registers/selects the private VFS. A live stock connection
// keeps committed data in a real WAL; the parent must not substitute a copy.
func TestPersistentSQLStockChild(t *testing.T) {
	p := os.Getenv("AGENT_PERSISTENT_SQL_FIXTURE")
	if p == "" {
		return
	}
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := db.Close(); e != nil {
			t.Errorf("stock DB close: %v", e)
		}
	}()
	db.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0", "CREATE TABLE fixture(value TEXT NOT NULL)", "INSERT INTO fixture VALUES ('committed-stock-wal')"} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, e := fmt.Println("STOCK_WAL_READY"); e != nil {
		t.Fatal(e)
	}
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() || scanner.Text() != "release" {
		t.Fatalf("fixture release missing/invalid: %v", scanner.Err())
	}
}

func TestPersistentSQLPrivateReadsExistingStockWAL(t *testing.T) {
	p := filepath.Join(t.TempDir(), "existing.db")
	cmd := exec.Command(os.Args[0], "-test.run=^TestPersistentSQLStockChild$", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), "AGENT_PERSISTENT_SQL_FIXTURE="+p)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		if e := stdout.Close(); e != nil {
			t.Errorf("fixture stdout setup close: %v", e)
		}
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	drained := make(chan error, 1)
	if err = cmd.Start(); err != nil {
		if e := stdin.Close(); e != nil {
			t.Errorf("fixture stdin start-failure close: %v", e)
		}
		if e := stdout.Close(); e != nil {
			t.Errorf("fixture stdout start-failure close: %v", e)
		}
		t.Fatal(err)
	}
	defer func() {
		if _, e := fmt.Fprintln(stdin, "release"); e != nil {
			t.Errorf("fixture release: %v", e)
		}
		if e := stdin.Close(); e != nil {
			t.Errorf("fixture stdin close: %v", e)
		}
		if e := <-drained; e != nil {
			t.Errorf("fixture stdout drain: %v", e)
		}
		if e := cmd.Wait(); e != nil {
			t.Errorf("stock fixture exit: %v", e)
		}
	}()
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- scanner.Text()
		} else {
			ready <- "EOF"
		}
		for scanner.Scan() {
		}
		drained <- scanner.Err()
	}()
	select {
	case line := <-ready:
		if line != "STOCK_WAL_READY" {
			t.Fatalf("fixture setup: %s", line)
		}
	case <-time.After(20 * time.Second):
		if e := cmd.Process.Kill(); e != nil {
			t.Errorf("fixture kill: %v", e)
		}
		t.Fatal("stock fixture ready timeout")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		info, e := os.Stat(p + suffix)
		if e != nil || info.Size() == 0 {
			t.Fatalf("fixture sidecar %s missing/empty: %v", suffix, e)
		}
	}
	stockRead := func() {
		t.Helper()
		db, e := sql.Open("sqlite", p)
		if e != nil {
			t.Fatal(e)
		}
		defer func() {
			if e := db.Close(); e != nil {
				t.Errorf("stock DB close: %v", e)
			}
		}()
		var got string
		if e = db.QueryRow("SELECT value FROM fixture").Scan(&got); e != nil || got != "committed-stock-wal" {
			t.Fatalf("default stock changed: value=%q error=%v", got, e)
		}
	}
	stockRead()
	if err = sqlite3.ExperimentalPersistentRegister(); err != nil {
		t.Fatalf("private registration setup: %v", err)
	}
	before := sqlite3.ExperimentalPersistentRefusals()
	db, err := sql.Open("sqlite", persistentSQLURI(p))
	if err != nil {
		t.Fatal(err)
	}
	var got string
	readErr := db.QueryRow("SELECT value FROM fixture").Scan(&got)
	closeErr := db.Close()
	stockRead() // Assert default isolation even while the positive contract is RED.
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	// Mandatory on BOTH success and failure: default-VFS fallback cannot pass.
	if sqlite3.ExperimentalPersistentRefusals() <= before {
		t.Fatalf("private xOpen not reached (setup failure or default fallback): value=%q error=%v", got, readErr)
	}
	if readErr != nil {
		t.Fatalf("PERSISTENT_SQL_EXPECTED_RED: real private xOpen refused existing stock WAL: %v", readErr)
	}
	if got != "committed-stock-wal" {
		t.Fatalf("private read returned %q", got)
	}
}
