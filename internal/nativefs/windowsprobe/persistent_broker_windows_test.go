//go:build windows && nativeprobe_sqlite_identity

package windowsprobe

import (
	"bytes"
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var persistentNames = []string{"store.db", "store.db-wal", "store.db-shm"}

func persistentFixture(t *testing.T) (string, *Root) {
	t.Helper()
	// Go temp paths can contain 8.3 ancestor aliases. Reuse the established
	// handle-derived canonical fixture path; production exactName stays strict.
	dir := localRootPath(t)
	for i, n := range append(append([]string{}, persistentNames...), "neighbor.txt") {
		if err := os.WriteFile(filepath.Join(dir, n), bytes.Repeat([]byte{byte(i + 1)}, 128+i), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := OpenLocalRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	t.Log("persistent fixture: canonical long-name OpenLocalRoot qualified; entering broker contract")
	return dir, root
}
func persistentNew(t *testing.T, r *Root, h persistentHooks) *persistentBroker {
	t.Helper()
	b, e := newPersistentBroker(r, "store.db", h)
	if e != nil {
		t.Fatalf("existing-object broker required: %v", e)
	}
	t.Cleanup(func() {
		if e := b.close(); e != nil {
			t.Error(e)
		}
	})
	return b
}
func persistentAcquireAll(t *testing.T, b *persistentBroker) {
	t.Helper()
	for role := persistentMain; role <= persistentSHM; role++ {
		if e := b.acquireExisting(context.Background(), role); e != nil {
			t.Fatalf("role %d: %v", role, e)
		}
	}
}
func persistentBytes(t *testing.T, dir string) [][]byte {
	t.Helper()
	var out [][]byte
	for _, name := range persistentNames {
		v, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil {
			t.Fatal(e)
		}
		out = append(out, v)
	}
	return out
}
func TestPersistentExistingObjectsAndExternalReaders(t *testing.T) {
	dir, r := persistentFixture(t)
	before := persistentBytes(t, dir)
	baseline := captureHandleInventory(t)
	b := persistentNew(t, r, persistentHooks{})
	persistentAcquireAll(t, b)
	if b.evidence().Published != 3 {
		t.Fatal("all roles not published")
	}
	// A separate ordinary reader must still open each live owned object. This is
	// byte-preservation/share-mode evidence, not SQLite lock interoperability.
	if !reflect.DeepEqual(before, persistentBytes(t, dir)) {
		t.Fatal("existing bytes changed")
	}
	for _, name := range persistentNames {
		if data, e := b.readWorkspace(context.Background(), r, []string{name}); !errors.Is(e, errProtected) || data != nil {
			t.Fatalf("protected role read %s: data=%d error=%v", name, len(data), e)
		}
	}
	if data, e := b.readWorkspace(context.Background(), r, []string{"neighbor.txt"}); e != nil || len(data) == 0 {
		t.Fatalf("neighbor: %v", e)
	}
	if e := b.close(); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, persistentBytes(t, dir)) {
		t.Fatal("close changed bytes")
	}
	if after := captureHandleInventory(t); !reflect.DeepEqual(baseline.files, after.files) {
		t.Fatalf("disk handles not restored: before=%v after=%v", baseline.files, after.files)
	}
}
func TestPersistentPublicationAndCancellation(t *testing.T) {
	_, r := persistentFixture(t)
	b := persistentNew(t, r, persistentHooks{})
	if data, e := b.readWorkspace(context.Background(), r, []string{"neighbor.txt"}); !errors.Is(e, errPersistentPending) || data != nil {
		t.Fatalf("unqualified broker admitted read: %v", e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := b.acquireExisting(ctx, persistentMain); !errors.Is(e, context.Canceled) {
		t.Fatalf("cancel: %v", e)
	}
	if b.evidence().Published != 0 {
		t.Fatal("cancelled acquisition published")
	}
	persistentAcquireAll(t, b)
	if e := b.acquireExisting(context.Background(), persistentMain); e == nil {
		t.Fatal("duplicate role accepted")
	}
	if e := b.acquireExisting(context.Background(), persistentRole(99)); e == nil {
		t.Fatal("unknown role accepted")
	}
}
func TestPersistentIndependentLineage(t *testing.T) {
	dir, r := persistentFixture(t)
	b := persistentNew(t, r, persistentHooks{})
	if e := r.Close(); e != nil {
		t.Fatal(e)
	}
	persistentAcquireAll(t, b)
	reader, e := OpenLocalRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := reader.Close(); e != nil {
			t.Error(e)
		}
	}()
	if data, e := b.readWorkspace(context.Background(), reader, []string{"store.db-wal"}); !errors.Is(e, errProtected) || data != nil {
		t.Fatalf("ownership: %v", e)
	}
}
func TestPersistentExistingRefusals(t *testing.T) {
	for _, kind := range []string{"missing", "directory", "hardlink", "case", "symlink", "junction"} {
		t.Run(kind, func(t *testing.T) {
			dir, r := persistentFixture(t)
			if e := os.Remove(filepath.Join(dir, "store.db")); e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "directory":
				if e := os.Mkdir(filepath.Join(dir, "store.db"), 0700); e != nil {
					t.Fatal(e)
				}
			case "hardlink":
				if e := os.Link(filepath.Join(dir, "neighbor.txt"), filepath.Join(dir, "store.db")); e != nil {
					t.Fatal(e)
				}
			case "symlink", "junction":
				target := t.TempDir()
				if kind == "symlink" {
					target = filepath.Join(target, "sentinel.txt")
					if e := os.WriteFile(target, []byte("outside sentinel"), 0600); e != nil {
						t.Fatal(e)
					}
					if e := os.Symlink(target, filepath.Join(dir, "store.db")); e != nil {
						t.Fatalf("required symlink fixture: %v", e)
					}
				} else {
					if output, e := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", filepath.Join(dir, "store.db"), target).CombinedOutput(); e != nil {
						t.Fatalf("required junction fixture: %v %s", e, output)
					}
				}
				// Remove only the link before TempDir's recursive cleanup.
				t.Cleanup(func() {
					if e := os.Remove(filepath.Join(dir, "store.db")); e != nil {
						t.Error(e)
					}
				})
				actual, e := os.Stat(filepath.Join(dir, "store.db"))
				if e != nil {
					t.Fatal(e)
				}
				expected, e := os.Stat(target)
				if e != nil {
					t.Fatal(e)
				}
				if !os.SameFile(actual, expected) {
					t.Fatal("reparse fixture did not reach intended target")
				}
			case "case":
				if e := os.WriteFile(filepath.Join(dir, "STORE.DB"), []byte("wrong spelling"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			b := persistentNew(t, r, persistentHooks{})
			if e := b.acquireExisting(context.Background(), persistentMain); e == nil {
				t.Fatalf("accepted %s", kind)
			}
			if b.evidence().Published != 0 {
				t.Fatal("unsafe candidate published")
			}
		})
	}
}
func TestPersistentInvalidBasenames(t *testing.T) {
	_, r := persistentFixture(t)
	cases := []struct{ label, name string }{
		{"empty", ""}, {"ads", "store.db:secret"}, {"relative-path", `sub\store.db`},
		{"absolute-path", `C:\store.db`}, {"parent", ".."}, {"device", "NUL.db"},
		{"trailing-dot", "store.db."}, {"trailing-space", "store.db "},
		{"sidecar-overflow", strings.Repeat("x", 252)}, {"nul", "store\x00.db"},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			b, e := newPersistentBroker(r, c.name, persistentHooks{})
			if b != nil {
				if ce := b.close(); ce != nil {
					t.Error(ce)
				}
				t.Fatal("invalid basename published broker")
			}
			if !errors.Is(e, errUnsafe) {
				t.Fatalf("expected lexical refusal, got %v", e)
			}
		})
	}
}
func TestPersistentMetadataSwap(t *testing.T) {
	dir, r := persistentFixture(t)
	swapped := false
	b := persistentNew(t, r, persistentHooks{afterMetadata: func(role persistentRole) error {
		if role != persistentMain {
			return nil
		}
		if e := os.Rename(filepath.Join(dir, "store.db"), filepath.Join(dir, "original.db")); e != nil {
			return e
		}
		if e := os.WriteFile(filepath.Join(dir, "store.db"), []byte("replacement sentinel"), 0600); e != nil {
			return e
		}
		swapped = true
		return nil
	}})
	if e := b.acquireExisting(context.Background(), persistentMain); e == nil {
		t.Fatal("metadata swap accepted")
	}
	if !swapped {
		t.Fatal("attack did not execute; refusal is not identity evidence")
	}
	if b.evidence().Published != 0 {
		t.Fatal("replacement published")
	}
	if v, e := os.ReadFile(filepath.Join(dir, "store.db")); e != nil || string(v) != "replacement sentinel" {
		t.Fatal("replacement changed")
	}
}
func TestPersistentPublicationCloseRace(t *testing.T) {
	_, r := persistentFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	b := persistentNew(t, r, persistentHooks{afterRegistered: func(role persistentRole) error {
		if role == persistentSHM {
			close(entered)
			<-release
		}
		return nil
	}})
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	for _, role := range []persistentRole{persistentMain, persistentWAL} {
		if e := b.acquireExisting(context.Background(), role); e != nil {
			t.Fatal(e)
		}
	}
	acquired := make(chan error, 1)
	go func() { acquired <- b.acquireExisting(context.Background(), persistentSHM) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("registration hook not reached")
	}
	closed := make(chan error, 1)
	go func() { closed <- b.close() }()
	select {
	case e := <-closed:
		t.Fatalf("close did not drain admitted acquisition: %v", e)
	case <-time.After(40 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	if e := <-acquired; e != nil {
		t.Fatal(e)
	}
	if e := <-closed; e != nil {
		t.Fatal(e)
	}
	if data, e := b.readWorkspace(context.Background(), r, []string{"neighbor.txt"}); !errors.Is(e, errPersistentClosed) || data != nil {
		t.Fatalf("closed admission: %v", e)
	}
}
func TestPersistentUncertainCloseQuarantines(t *testing.T) {
	_, r := persistentFixture(t)
	fault := errors.New("injected outcome uncertainty")
	var mu sync.Mutex
	calls := map[windows.Handle]int{}
	b, e := newPersistentBroker(r, "store.db", persistentHooks{close: func(h windows.Handle) error {
		mu.Lock()
		calls[h]++
		mu.Unlock()
		return errors.Join(windows.CloseHandle(h), fault)
	}})
	if e != nil {
		t.Fatalf("existing-object broker required: %v", e)
	}
	// Cleanup also covers early test failure; uncertainty is the expected terminal result.
	t.Cleanup(func() {
		if e := b.close(); e != nil && !errors.Is(e, fault) {
			t.Error(e)
		}
	})
	persistentAcquireAll(t, b)
	if e := b.close(); !errors.Is(e, fault) {
		t.Fatalf("lost uncertain outcome: %v", e)
	}
	if e := b.close(); !errors.Is(e, fault) {
		t.Fatalf("forgot quarantine: %v", e)
	}
	for h, n := range calls {
		if n != 1 {
			t.Fatalf("uncertain close retried handle=%x n=%d", h, n)
		}
	}
	if len(calls) < 3 {
		t.Fatal("owned handles did not reach close")
	}
	if !b.evidence().Closed || !errors.Is(b.evidence().Terminal, fault) {
		t.Fatal("terminal evidence missing")
	}
	if e := b.acquireExisting(context.Background(), persistentMain); !errors.Is(e, errPersistentClosed) {
		t.Fatalf("reopened poisoned broker: %v", e)
	}
	if data, e := b.readWorkspace(context.Background(), r, []string{"neighbor.txt"}); !errors.Is(e, errPersistentClosed) || data != nil {
		t.Fatalf("poisoned read: %v", e)
	}
}
