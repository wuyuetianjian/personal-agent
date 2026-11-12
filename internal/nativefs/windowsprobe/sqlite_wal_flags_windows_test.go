//go:build windows && nativeprobe_sqlite_wal_flags

package windowsprobe

import (
	"bufio"
	"database/sql"
	"fmt"
	"modernc.org/libc"
	_ "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const walFlagsChildEnv = "AGENT_STOCK_WAL_FLAGS_FIXTURE"

func walFlagsURI(path, mode, vfs string) string {
	u := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(path)}
	q := url.Values{"mode": {mode}}
	if vfs != "" {
		q.Set("vfs", vfs)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
func walFlagsChild(t *testing.T) bool {
	p := os.Getenv(walFlagsChildEnv)
	if p == "" {
		return false
	}
	db, e := sql.Open("sqlite", p)
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
		t.Fatalf("child WAL %s %v", mode, e)
	}
	for _, q := range []string{"PRAGMA wal_autocheckpoint=0", "CREATE TABLE fixture(value TEXT)"} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	var busy, logFrames, checkpointed int
	if e = db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed); e != nil || busy != 0 || logFrames != 0 || checkpointed != 0 {
		t.Fatalf("baseline checkpoint %d/%d/%d %v", busy, logFrames, checkpointed, e)
	}
	if _, e = db.Exec("INSERT INTO fixture VALUES('committed-wal-only')"); e != nil {
		t.Fatal(e)
	}
	if _, e = fmt.Println("STOCK_FLAGS_READY"); e != nil {
		t.Fatal(e)
	}
	scan := bufio.NewScanner(os.Stdin)
	if !scan.Scan() || scan.Text() != "release" {
		t.Fatalf("child release %v", scan.Err())
	}
	return true
}
func walFlagsFixture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "flags.db")
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), walFlagsChildEnv+"="+p)
	cmd.Stderr = os.Stderr
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	stdin, e := cmd.StdinPipe()
	if e != nil {
		_ = stdout.Close()
		t.Fatal(e)
	}
	if e = cmd.Start(); e != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		t.Fatal(e)
	}
	ready := make(chan string, 1)
	drained := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		s := bufio.NewScanner(stdout)
		if s.Scan() {
			ready <- s.Text()
		} else {
			ready <- "EOF"
		}
		for s.Scan() {
		}
		drained <- s.Err()
	}()
	// Wait begins only after the pipe reader drains, avoiding Wait closing its pipe.
	go func() {
		e := <-drained
		if e != nil {
			_ = cmd.Process.Kill()
		}
		w := cmd.Wait()
		if e != nil {
			done <- e
		} else {
			done <- w
		}
	}()
	t.Cleanup(func() {
		if _, e := fmt.Fprintln(stdin, "release"); e != nil {
			t.Error(e)
		}
		if e := stdin.Close(); e != nil {
			t.Error(e)
		}
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(10 * time.Second):
			if e := cmd.Process.Kill(); e != nil {
				t.Error(e)
			}
			select {
			case e := <-done:
				t.Errorf("fixture forced shutdown: %v", e)
			case <-time.After(5 * time.Second):
				t.Error("fixture shutdown unconfirmed")
			}
		}
	})
	select {
	case line := <-ready:
		if line != "STOCK_FLAGS_READY" {
			t.Fatalf("child setup %q", line)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("child setup timeout")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		s, e := os.Stat(p + suffix)
		if e != nil || s.Size() == 0 {
			t.Fatalf("stock fixture %s: %v", suffix, e)
		}
	}
	// immutable explicitly reads the main file alone, only as a fixture control.
	uri := walFlagsURI(p, "ro", "") + "&immutable=1"
	db, e := sql.Open("sqlite", uri)
	if e != nil {
		t.Fatal(e)
	}
	var count int
	e = db.QueryRow("SELECT count(*) FROM fixture WHERE value='committed-wal-only'").Scan(&count)
	ce := db.Close()
	if e != nil || ce != nil || count != 0 {
		t.Fatalf("WAL-only control count=%d err=%v close=%v", count, e, ce)
	}
	return p
}
func walFlagsStockRead(t *testing.T, p string) {
	t.Helper()
	db, e := sql.Open("sqlite", walFlagsURI(p, "ro", ""))
	if e != nil {
		t.Fatal(e)
	}
	var value string
	e = db.QueryRow("SELECT value FROM fixture").Scan(&value)
	ce := db.Close()
	if e != nil || ce != nil || value != "committed-wal-only" {
		t.Fatalf("stock positive %q %v %v", value, e, ce)
	}
}
func walFlagsCase(t *testing.T, mode string) {
	if walFlagsChild(t) {
		return
	}
	p := walFlagsFixture(t)
	walFlagsStockRead(t, p)
	tls := libc.NewTLS()
	defer tls.Close()
	defaultVFS := sqlite3.Xsqlite3_vfs_find(tls, 0)
	v, name, e := sqlite3.ExperimentalWALFlagsRegister()
	if e != nil {
		t.Fatalf("diagnostic registration setup: %v", e)
	}
	db, e := sql.Open("sqlite", walFlagsURI(p, mode, name))
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	var value string
	e = db.QueryRow("SELECT value FROM fixture").Scan(&value)
	ce := db.Close()
	walFlagsStockRead(t, p)
	if defaultVFS == 0 || sqlite3.Xsqlite3_vfs_find(tls, 0) != defaultVFS {
		t.Fatal("diagnostic changed default VFS pointer")
	}
	events := sqlite3.ExperimentalWALFlagsEvents(v)
	if len(events) == 0 {
		t.Fatal("diagnostic callback not reached")
	}
	if ce != nil {
		t.Fatal(ce)
	}
	if e != nil {
		for _, event := range events {
			if !event.Forwarded && event.RC == sqlite3.SQLITE_CANTOPEN {
				t.Fatalf("DIAGNOSTIC_OPEN_EXPECTED_RED: actual nondefault xOpen refusal input=%#x name=%q error=%v", event.Input, event.Name, e)
			}
		}
		t.Fatalf("forwarded diagnostic SQL failed: %v", e)
	}
	if value != "committed-wal-only" {
		t.Fatalf("diagnostic value %q", value)
	}
	mainCount, walCount := 0, 0
	for _, event := range events {
		t.Logf("STOCK_WAL_FLAGS mode=%s input=%#x output=%#x rc=%d forwarded=%t name=%q", mode, event.Input, event.Output, event.RC, event.Forwarded, event.Name)
		if !event.Forwarded || event.RC != sqlite3.SQLITE_OK {
			t.Fatal("nonstock or failed open in positive diagnostic")
		}
		if event.Input&sqlite3.SQLITE_OPEN_MAIN_DB != 0 {
			mainCount++
			want := int32(sqlite3.SQLITE_OPEN_READONLY)
			if mode == "rw" {
				want = sqlite3.SQLITE_OPEN_READWRITE
			}
			if event.Input&(sqlite3.SQLITE_OPEN_READONLY|sqlite3.SQLITE_OPEN_READWRITE) != want || event.Output != want {
				t.Fatalf("main flags %#x/%#x", event.Input, event.Output)
			}
		}
		if event.Input&sqlite3.SQLITE_OPEN_WAL != 0 {
			walCount++
			if !strings.HasSuffix(event.Name, "-wal") || event.Input != sqlite3.SQLITE_OPEN_READWRITE|sqlite3.SQLITE_OPEN_CREATE|sqlite3.SQLITE_OPEN_WAL || event.Output != sqlite3.SQLITE_OPEN_READWRITE {
				t.Fatalf("unexpected stock WAL flags %#x/%#x", event.Input, event.Output)
			}
		}
	}
	if len(events) != 2 || mainCount != 1 || walCount != 1 {
		t.Fatalf("expected exactly main+WAL observations: %d/%d", mainCount, walCount)
	}
}
func TestStockWALFlagsReadonlyMain(t *testing.T)  { walFlagsCase(t, "ro") }
func TestStockWALFlagsReadWriteMain(t *testing.T) { walFlagsCase(t, "rw") }
