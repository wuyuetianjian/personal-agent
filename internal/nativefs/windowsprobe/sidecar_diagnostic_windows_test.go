//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sidecar_diagnostic

package windowsprobe

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	_ "modernc.org/sqlite"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Child is selected only by our exact subprocess command, never by the native
// diagnostic manifest. All paths/payloads are generated in these temporary tests.
func TestSidecarDiagnosticChild(t *testing.T) {
	name := os.Getenv("AGENT_SIDECAR_DIAGNOSTIC_DB")
	if name == "" {
		t.Fatal("child requires isolated parent fixture")
	}
	db, e := sql.Open("sqlite", name)
	if e != nil {
		t.Fatal(e)
	}
	closed := false
	defer func() {
		if !closed {
			if e := db.Close(); e != nil {
				t.Error(e)
			}
		}
	}()
	db.SetMaxOpenConns(1)
	var mode string
	if e = db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode); e != nil || mode != "wal" {
		t.Fatalf("WAL setup %q %v", mode, e)
	}
	for _, q := range []string{"PRAGMA wal_autocheckpoint=0", "CREATE TABLE IF NOT EXISTS fixture(v TEXT)"} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	payload := os.Getenv("AGENT_SIDECAR_DIAGNOSTIC_PAYLOAD")
	if payload == "" {
		t.Fatal("empty synthetic payload")
	}
	if _, e = db.Exec("INSERT INTO fixture VALUES (?)", payload); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = db.QueryRow("SELECT count(*) FROM fixture WHERE v=?", payload).Scan(&n); e != nil || n != 1 {
		t.Fatalf("stock committed control %d %v", n, e)
	}
	if _, e = fmt.Println("SIDECAR_CHILD_READY"); e != nil {
		t.Fatal(e)
	}
	s := bufio.NewScanner(os.Stdin)
	if !s.Scan() {
		t.Fatalf("child command: %v", s.Err())
	}
	switch s.Text() {
	case "crash":
		os.Exit(23)
	case "close":
		closed = true
		if e = db.Close(); e != nil {
			t.Fatal(e)
		}
	default:
		t.Fatal("invalid child command")
	}
}

type sidecarChildResult struct {
	scan, wait error
	output     string
}

type sidecarChild struct {
	cmd      *exec.Cmd
	in       io.WriteCloser
	done     chan sidecarChildResult
	finished bool
	cancel   context.CancelFunc
}

func startSidecarChild(t *testing.T, name, payload string) *sidecarChild {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSidecarDiagnosticChild$", "-test.timeout=40s")
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(os.Environ(), "AGENT_SIDECAR_DIAGNOSTIC_DB="+name, "AGENT_SIDECAR_DIAGNOSTIC_PAYLOAD="+payload)
	out, e := cmd.StdoutPipe()
	if e != nil {
		cancel()
		t.Fatal(e)
	}
	in, e := cmd.StdinPipe()
	if e != nil {
		cancel()
		if ce := out.Close(); ce != nil {
			t.Error(ce)
		}
		t.Fatal(e)
	}
	cmd.Stderr = os.Stderr
	if e = cmd.Start(); e != nil {
		cancel()
		if ce := in.Close(); ce != nil {
			t.Error(ce)
		}
		if ce := out.Close(); ce != nil {
			t.Error(ce)
		}
		t.Fatal(e)
	}
	c := &sidecarChild{cmd: cmd, in: in, done: make(chan sidecarChildResult, 1), cancel: cancel}
	ready := make(chan struct{}, 1)
	go func() {
		s := bufio.NewScanner(out)
		var output bytes.Buffer
		for s.Scan() {
			if output.Len() < 65536 {
				fmt.Fprintln(&output, s.Text())
			}
			if s.Text() == "SIDECAR_CHILD_READY" {
				select {
				case ready <- struct{}{}:
				default:
				}
			}
		}
		se := s.Err()
		c.done <- sidecarChildResult{se, cmd.Wait(), output.String()}
	}()
	t.Cleanup(func() {
		defer cancel()
		if c.in != nil {
			if e := c.in.Close(); e != nil {
				t.Error(e)
			}
			c.in = nil
		}
		if !c.finished {
			if e := cmd.Process.Kill(); e != nil && !errors.Is(e, os.ErrProcessDone) {
				t.Error(e)
			}
			select {
			case result := <-c.done:
				if result.scan != nil {
					t.Error(result.scan)
				}
				c.finished = true
			case <-time.After(10 * time.Second):
				t.Error("child reap timeout")
			}
		}
	})
	select {
	case <-ready:
		return c
	case e := <-c.done:
		c.finished = true
		t.Fatalf("child failed before ready: %v / %v; %s", e.scan, e.wait, e.output)
	case <-ctx.Done():
		t.Fatalf("child readiness timeout: %v", ctx.Err())
	}
	return nil
}
func (c *sidecarChild) finish(t *testing.T, command string) {
	t.Helper()
	if _, e := fmt.Fprintln(c.in, command); e != nil {
		t.Fatal(e)
	}
	e := c.in.Close()
	c.in = nil
	if e != nil {
		t.Fatal(e)
	}
	select {
	case result := <-c.done:
		c.finished = true
		c.cancel()
		if result.scan != nil {
			t.Fatalf("child output: %v", result.scan)
		}
		e := result.wait
		if command == "crash" {
			var x *exec.ExitError
			if !errors.As(e, &x) || x.ExitCode() != 23 {
				t.Fatalf("expected synthetic crash exit23: %v", e)
			}
		} else if e != nil {
			t.Fatalf("stock normal close: %v; %s", e, result.output)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("child shutdown timeout")
	}
}
func sidecarRoot(t *testing.T, dir string) *Root {
	t.Helper()
	r, e := OpenLocalRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := r.Close(); e != nil {
			t.Error(e)
		}
	})
	return r
}
func sidecarPin(t *testing.T, r *Root, name string) (windows.Handle, snapshot) {
	t.Helper()
	h, e := persistentOpen(r.h, name, false)
	if h != 0 && h != windows.InvalidHandle {
		t.Cleanup(func() {
			if ce := windows.CloseHandle(h); ce != nil {
				t.Error(ce)
			}
		})
	}
	if e != nil {
		t.Fatal(e)
	}
	s, e := inspect(h)
	if e != nil {
		t.Fatal(e)
	}
	if e = exactName(h, name); e != nil {
		t.Fatal(e)
	}
	return h, s
}
func sidecarNeighbor(t *testing.T, r *Root, dir string, excluded map[idInfo]struct{}) {
	t.Helper()
	if e := os.WriteFile(filepath.Join(dir, "neighbor.txt"), []byte("ordinary neighbor"), 0600); e != nil {
		t.Fatal(e)
	}
	b, e := r.read(context.Background(), []string{"neighbor.txt"}, excluded)
	if e != nil || string(b) != "ordinary neighbor" {
		t.Fatalf("ordinary neighbor control: %v", e)
	}
}

func TestSidecarDiagnosticCurrentGuards(t *testing.T) {
	dir := localRootPath(t)
	name := filepath.Join(dir, "store.db")
	child := startSidecarChild(t, name, "current-guard-synthetic")
	r := sidecarRoot(t, dir)
	b := persistentNew(t, r, persistentHooks{})
	if data, e := b.readWorkspace(context.Background(), r, []string{"neighbor.txt"}); !errors.Is(e, errPersistentPending) || len(data) != 0 {
		t.Fatalf("missing guard admission %v", e)
	}
	persistentAcquireAll(t, b)
	sidecarNeighbor(t, r, dir, nil)
	if data, e := b.readWorkspace(context.Background(), r, []string{"neighbor.txt"}); e != nil || string(data) != "ordinary neighbor" {
		t.Fatalf("broker neighbor %v", e)
	}
	for _, n := range persistentNames {
		if data, e := b.readWorkspace(context.Background(), r, []string{n}); !errors.Is(e, errProtected) || len(data) != 0 {
			t.Fatalf("registered identity control %v", e)
		}
	}
	child.finish(t, "close")
	wal := name + "-wal"
	retired := filepath.Join(dir, "guarded-retired.bin")
	e := os.Rename(wal, retired)
	if !errors.Is(e, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("expected native retirement sharing violation, not arbitrary refusal: %v", e)
	}
	if e = b.close(); e != nil {
		t.Fatal(e)
	}
	if data, e := b.readWorkspace(context.Background(), r, []string{"neighbor.txt"}); !errors.Is(e, errPersistentClosed) || len(data) != 0 {
		t.Fatalf("closed broker admission %v", e)
	}
	if e = os.Rename(wal, retired); e != nil {
		t.Fatalf("released retirement positive control: %v", e)
	}
	// A separate clean stock lifecycle is not affected by experimental guards.
	fresh := startSidecarChild(t, filepath.Join(dir, "unguarded.db"), "unguarded-close")
	fresh.finish(t, "close")
	t.Log("SIDECAR_CURRENT_GUARDS_REFUSE: missing/closed admission refuses, protected IDs refuse, ordinary neighbor reads, guard retirement blocks then succeeds after release")
}

func TestSidecarDiagnosticUnknownRetiredWAL(t *testing.T) {
	dir := localRootPath(t)
	name := filepath.Join(dir, "store.db")
	initial := startSidecarChild(t, name, "baseline-only")
	initial.finish(t, "close")
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, e := os.Stat(name + suffix); !errors.Is(e, os.ErrNotExist) {
			t.Fatalf("baseline sidecar not retired: %s %v", suffix, e)
		}
	}
	r := sidecarRoot(t, dir)
	_, main := sidecarPin(t, r, "store.db")
	excluded := map[idInfo]struct{}{main.ID: {}}
	payload := "synthetic-secret-only-in-fresh-wal-" + filepath.Base(dir)
	child := startSidecarChild(t, name, payload)
	// Test observer pins do not silently register fresh identities in the model.
	_, wal := sidecarPin(t, r, "store.db-wal")
	_, shm := sidecarPin(t, r, "store.db-shm")
	if wal.ID == main.ID || shm.ID == main.ID || wal.Standard.Size <= 32 || wal.Standard.Size > Limit {
		t.Fatal("invalid WAL fixture identity/size")
	}
	child.finish(t, "crash")
	raw, e := os.ReadFile(name + "-wal")
	if e != nil {
		t.Fatal(e)
	}
	if len(raw) < 32 || !(bytes.Equal(raw[:4], []byte{0x37, 0x7f, 0x06, 0x82}) || bytes.Equal(raw[:4], []byte{0x37, 0x7f, 0x06, 0x83})) || !bytes.Contains(raw, []byte(payload)) {
		t.Fatal("real WAL payload/header control failed")
	}
	mainBytes, e := os.ReadFile(name)
	if e != nil || bytes.Contains(mainBytes, []byte(payload)) {
		t.Fatalf("WAL-only main control: %v", e)
	}
	neighbor := "ordinary-notes.bin"
	if e = os.Rename(name+"-wal", filepath.Join(dir, neighbor)); e != nil {
		t.Fatalf("external fresh WAL retirement: %v", e)
	}
	_, retired := sidecarPin(t, r, neighbor)
	if retired.ID != wal.ID || retired.Standard.Links != 1 {
		t.Fatal("rename not same single-link identity")
	}
	if _, known := excluded[retired.ID]; known {
		t.Fatal("fixture accidentally registered retired WAL")
	}
	sidecarNeighbor(t, r, dir, excluded)
	// This is intentionally NOT b.readWorkspace. It models relaxed admission
	// after restrictive guards are released; the current broker refuses that state.
	got, e := r.read(context.Background(), []string{neighbor}, excluded)
	if e != nil || !bytes.Contains(got, []byte(payload)) {
		t.Fatalf("candidate gap not reproduced (inconclusive), err=%v", e)
	}
	excluded[retired.ID] = struct{}{}
	got, e = r.read(context.Background(), []string{neighbor}, excluded)
	if !errors.Is(e, errProtected) || len(got) != 0 {
		t.Fatalf("registered retired ID control: %v", e)
	}
	sidecarNeighbor(t, r, dir, excluded)
	t.Log("SIDECAR_PROVENANCE_GAP_CONFIRMED: simulated relaxed known-ID policy reads renamed fresh WAL; registering its real ID blocks bytes; current broker remains closed/pending fail-closed. NOT SECURITY GREEN")
}
