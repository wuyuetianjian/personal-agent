//go:build windows && nativeprobe_sqlite_identity

package rootlease

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent/internal/nativefs/windowsprobe"
	"agent/internal/storage"
)

const privateDriverName = "sqlite-nativeprobe-private"

type privateFactoryControl interface {
	ExperimentalValidateFilename(string) error
	ExperimentalEnableSharedCacheForSubprocess() error
}

func privateFactory(t *testing.T) privateFactoryControl {
	t.Helper()
	db, err := sql.Open(privateDriverName, "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	control, ok := db.Driver().(privateFactoryControl)
	if !ok {
		t.Fatal("private factory control missing")
	}
	return control
}
func privateOpen(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open(privateDriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("WAL init mode=%s error=%v", mode, err)
	}
	return db
}

// This uses the controlled experimental driver, NOT production storage.Open.
// storage.DB is only the actual SQL consumer/lifecycle wrapper.
func privateWALFixture(t *testing.T) (*storage.DB, *registry, binding, *sqliteIdentityBackend) {
	t.Helper()
	dir := nativeLongFixturePath(t)
	db := &storage.DB{SQL: privateOpen(t, filepath.Join(dir, "notes.txt"))}
	if _, err := db.SQL.Exec("CREATE TABLE identity_probe(value INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "neighbor.txt"), []byte("ordinary neighbor\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var native *sqliteIdentityBackend
	reg := fixtureRegistry(t, 1, func(ctx context.Context, path string) (backend, error) {
		root, err := windowsprobe.OpenLocalRoot(path)
		if err != nil {
			return nil, err
		}
		native = &sqliteIdentityBackend{root: root, db: db.SQL, includeJournal: true}
		return native, nil
	})
	p, err := reg.acquire(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	b := binding{reference: p.reference(), path: dir, scope: []byte("controlled private-cache current-WAL experiment"), policy: []byte("not complete DB protection")}
	if err := p.activate(b); err != nil {
		t.Fatal(err)
	}
	return db, reg, b, native
}
func TestNativeSQLitePrivateFactoryAcceptsAbsoluteFilename(t *testing.T) {
	path := filepath.Join(nativeLongFixturePath(t), "plain.db")
	if err := privateFactory(t).ExperimentalValidateFilename(path); err != nil {
		t.Fatalf("valid fixture rejected: %v", err)
	}
	db := privateOpen(t, path)
	if _, err := db.Exec("CREATE TABLE factory_test(value)"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
func TestNativeSQLitePrivateFactoryRejectsUntrustedDSN(t *testing.T) {
	control := privateFactory(t)
	for _, input := range []string{
		"", ":memory:", "file:C:/db?cache=shared", `relative.db`, `C:relative.db`,
		`\\server\share\db`, `\\?\C:\db`, `\\.\C:\db`, `C:\x?cache=shared`, `C:\x#fragment`, "C:\\x\x00.db",
		`C:\a\..\db`, `C:\a\.\db`, `C:\a\\db`, `C:\a\db:stream`, `C:\a\NUL`, `C:\a\db.`, `C:\a\db `, `C:\`,
		"C:\\a\\bad" + string([]byte{0xff}), "C:\\a\\bad\x01", "C:\\a\\bad\x7f",
		`C:\a\bad<`, `C:\a\bad>`, `C:\a\bad|`, `C:\a\bad*`, `C:\a\bad"`,
		`C:\a\con`, `C:\a\CoN.txt`, `C:\a\prn.log`, `C:\a\AuX`, `C:\a\nul.ext`,
		`C:\a\CONIN$`, `C:\a\conout$.txt`, `C:\a\CLOCK$`, `C:\a\com1.txt`, `C:\a\COM9`,
		`C:\a\lPt1.log`, `C:\a\LPT9`, `C:\a\COM¹`, `C:\a\com².txt`, `C:\a\COM³`, `C:\a\LPT¹`, `C:\a\lpt².txt`, `C:\a\LPT³`,
	} {
		if err := control.ExperimentalValidateFilename(input); err == nil {
			t.Fatalf("unsafe DSN accepted by pure validator: %q", input)
		}
	}
	// Never pass dangerous names to Open: the validator must be side-effect free.
	// Check constructor rejection too, but only with a path inside our fixture.
	db, err := sql.Open(privateDriverName, filepath.Join(nativeLongFixturePath(t), "rejected.db")+"?cache=shared")
	if err != nil {
		return
	}
	defer db.Close()
	if err := db.Ping(); err == nil {
		t.Fatal("Open accepted query injection")
	}
}
func TestNativeSQLitePrivateFactoryEncodesLiteralFilename(t *testing.T) {
	path := filepath.Join(nativeLongFixturePath(t), "space 雪 %3Fcache=shared.db")
	db := privateOpen(t, path)
	if _, err := db.Exec("CREATE TABLE literal_filename(value)"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("encoded path did not create literal filename: %v", err)
	}
	rows, err := db.Query("PRAGMA database_list")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var n int
		var name, file string
		if err := rows.Scan(&n, &name, &file); err != nil {
			t.Fatal(err)
		}
		if name == "main" {
			a, e := os.Stat(path)
			b, f := os.Stat(file)
			if e != nil || f != nil || !os.SameFile(a, b) {
				t.Fatalf("SQLite main is not literal fixture: %v %v", e, f)
			}
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("missing main")
	}
}
func fixtureSQLiteURI(path, cache string) string {
	return (&url.URL{Scheme: "file", Path: "/" + strings.ReplaceAll(path, `\`, "/"), RawQuery: url.Values{"cache": {cache}, "vfs": {"win32"}}.Encode()}).String()
}
func TestNativeSQLitePrivateFactoryJournalInterfaceBoundary(t *testing.T) {
	dir := nativeLongFixturePath(t)
	for _, dsn := range []string{filepath.Join(dir, "ordinary.db"), fixtureSQLiteURI(filepath.Join(dir, "shared.db"), "shared")} {
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			t.Fatal(err)
		}
		conn, err := db.Conn(context.Background())
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
		err = conn.Raw(func(raw any) error {
			if _, ok := raw.(experimentalJournalHandles); ok {
				return errors.New("ordinary/shared driver exposes journal authority")
			}
			return nil
		})
		closeErr := errors.Join(conn.Close(), db.Close())
		if err != nil || closeErr != nil {
			t.Fatalf("boundary: %v / %v", err, closeErr)
		}
	}
	db := privateOpen(t, filepath.Join(dir, "controlled.db"))
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Raw(func(raw any) error {
		if _, ok := raw.(experimentalJournalHandles); !ok {
			return errors.New("controlled wrapper lacks journal method")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestNativeSQLitePrivateFactoryDriverInterfaces(t *testing.T) {
	db := privateOpen(t, filepath.Join(nativeLongFixturePath(t), "interfaces.db"))
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		checks := []bool{}
		_, a := raw.(driver.Conn)
		checks = append(checks, a)
		_, a = raw.(driver.ConnBeginTx)
		checks = append(checks, a)
		_, a = raw.(driver.ConnPrepareContext)
		checks = append(checks, a)
		_, a = raw.(driver.ExecerContext)
		checks = append(checks, a)
		_, a = raw.(driver.QueryerContext)
		checks = append(checks, a)
		_, a = raw.(driver.Pinger)
		checks = append(checks, a)
		_, a = raw.(driver.SessionResetter)
		checks = append(checks, a)
		_, a = raw.(driver.Validator)
		checks = append(checks, a)
		for i, ok := range checks {
			if !ok {
				return fmt.Errorf("missing promoted driver interface %d", i)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), "CREATE TABLE interfaces(value)"); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO interfaces VALUES (1)"); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func TestNativeSQLitePrivateFactoryOverridesGlobalSharedCache(t *testing.T) {
	if err := privateFactory(t).ExperimentalEnableSharedCacheForSubprocess(); err == nil {
		t.Fatal("parent process allowed global shared-cache control")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := nativeLongFixturePath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestPrivateCacheChildHelper$", "-test.v", "-test.timeout=45s")
	cmd.Dir = dir
	cmd.Env = []string{"SQLITE_PRIVATE_CACHE_CHILD=qualification-v1", "SQLITE_PRIVATE_CACHE_FIXTURE=" + dir}
	out, err := cmd.CombinedOutput()
	t.Logf("private-cache child:\n%s", out)
	if err != nil {
		t.Fatalf("private-cache subprocess: %v", err)
	}
	if !bytes.Contains(out, []byte("PRIVATE_EXPLICIT_SHARED_AND_GLOBAL_DEFAULT_OK")) {
		t.Fatal("missing complete child proof marker")
	}
}
func TestPrivateCacheChildHelper(t *testing.T) {
	if os.Getenv("SQLITE_PRIVATE_CACHE_CHILD") == "" {
		return
	}
	if os.Getenv("SQLITE_PRIVATE_CACHE_CHILD") != "qualification-v1" {
		t.Fatal("invalid child marker")
	}
	dir := os.Getenv("SQLITE_PRIVATE_CACHE_FIXTURE")
	if dir == "" {
		t.Fatal("missing fixture")
	}
	if err := privateFactory(t).ExperimentalEnableSharedCacheForSubprocess(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"private", "explicit-shared", "global-default"} {
		name := "sqlite"
		path := filepath.Join(dir, mode+".db")
		dsn := path
		if mode == "private" {
			name = privateDriverName
		}
		if mode == "explicit-shared" {
			dsn = fixtureSQLiteURI(path, "shared")
		}
		db, err := sql.Open(name, dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		db.SetMaxOpenConns(2)
		if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("CREATE TABLE snapshot(value); INSERT INTO snapshot VALUES (1)"); err != nil {
			t.Fatal(err)
		}
		a, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		b, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		for _, c := range []*sql.Conn{a, b} {
			if _, err := c.ExecContext(context.Background(), "PRAGMA read_uncommitted=1; PRAGMA busy_timeout=0"); err != nil {
				t.Fatal(err)
			}
		}
		tx, err := a.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec("UPDATE snapshot SET value=2"); err != nil {
			t.Fatal(err)
		}
		var value int
		err = b.QueryRowContext(context.Background(), "SELECT value FROM snapshot").Scan(&value)
		want := 2 // both shared cases must observe the uncommitted update
		if mode == "private" {
			want = 1
		}
		if err != nil || value != want {
			t.Fatalf("cache mode=%s expected=%d observed=%d error=%v", mode, want, value, err)
		}
		t.Logf("cache mode=%s observed expected snapshot=%d", mode, value)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("PRIVATE_EXPLICIT_SHARED_AND_GLOBAL_DEFAULT_OK")
}
