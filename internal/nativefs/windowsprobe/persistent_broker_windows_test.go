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

func persistentMissing(t *testing.T, role persistentRole, hooks persistentHooks) (string, *Root, *persistentBroker) {
	t.Helper()
	dir, r := persistentFixture(t)
	if e := os.Remove(filepath.Join(dir, persistentNames[role])); e != nil {
		t.Fatal(e)
	}
	b, e := newPersistentBroker(r, "store.db", hooks)
	if e != nil {
		t.Fatal(e)
	}
	for _, existing := range []persistentRole{persistentMain, persistentWAL, persistentSHM} {
		if existing != role {
			if e := b.acquireExisting(context.Background(), existing); e != nil {
				_ = b.close()
				t.Fatal(e)
			}
		}
	}
	return dir, r, b
}
func TestPersistentCreateMissingSidecar(t *testing.T) {
	for _, role := range []persistentRole{persistentWAL, persistentSHM} {
		t.Run(persistentNames[role], func(t *testing.T) {
			dir, r, b := persistentMissing(t, role, persistentHooks{})
			t.Cleanup(func() {
				if e := b.close(); e != nil {
					t.Error(e)
				}
			})
			mainBefore, e := os.ReadFile(filepath.Join(dir, persistentNames[0]))
			if e != nil {
				t.Fatal(e)
			}
			if _, e := b.readWorkspace(context.Background(), r, []string{"neighbor.txt"}); !errors.Is(e, errPersistentPending) {
				t.Fatalf("missing role admitted read: %v", e)
			}
			result, e := b.createMissing(context.Background(), role)
			if e != nil || !result.Created || !result.Registered {
				t.Fatalf("missing sidecar creation required: result=%+v err=%v", result, e)
			}
			if b.evidence().Published != 3 {
				t.Fatal("created identity not registered")
			}
			v, e := os.ReadFile(filepath.Join(dir, persistentNames[role]))
			if e != nil || len(v) != 0 {
				t.Fatalf("new sidecar not empty/read-shareable: %v", e)
			}
			if data, e := b.readWorkspace(context.Background(), r, []string{persistentNames[role]}); data != nil || !errors.Is(e, errProtected) {
				t.Fatalf("created role readable: %v", e)
			}
			if data, e := b.readWorkspace(context.Background(), r, []string{"neighbor.txt"}); e != nil || len(data) == 0 {
				t.Fatalf("neighbor: %v", e)
			}
			if e = b.close(); e != nil {
				t.Fatal(e)
			}
			if v, e = os.ReadFile(filepath.Join(dir, persistentNames[role])); e != nil || len(v) != 0 {
				t.Fatalf("close removed/modified residual: %v", e)
			}
			if v, e = os.ReadFile(filepath.Join(dir, persistentNames[0])); e != nil || !bytes.Equal(v, mainBefore) {
				t.Fatal("main changed")
			}
		})
	}
}
func TestPersistentCreateCollisionPreservesThirdParty(t *testing.T) {
	var dir string
	called := false
	hooks := persistentHooks{beforeCreate: func(role persistentRole) error {
		called = true
		return os.WriteFile(filepath.Join(dir, persistentNames[role]), []byte("third-party sentinel"), 0600)
	}}
	path, _, b := persistentMissing(t, persistentWAL, hooks)
	dir = path
	t.Cleanup(func() {
		if e := b.close(); e != nil {
			t.Error(e)
		}
	})
	result, e := b.createMissing(context.Background(), persistentWAL)
	if e == nil || result.Created || result.Registered {
		t.Fatalf("collision accepted: %+v %v", result, e)
	}
	if !called {
		t.Fatal("collision control did not execute")
	}
	if v, e := os.ReadFile(filepath.Join(dir, "store.db-wal")); e != nil || string(v) != "third-party sentinel" {
		t.Fatalf("collision destroyed: %v", e)
	}
	if b.evidence().Published != 2 {
		t.Fatal("collision registered third-party object")
	}
}
func TestPersistentCreateRejectsMainAndInvalidRole(t *testing.T) {
	_, r := persistentFixture(t)
	b := persistentNew(t, r, persistentHooks{})
	for _, role := range []persistentRole{persistentMain, persistentRole(99)} {
		result, e := b.createMissing(context.Background(), role)
		if !errors.Is(e, errUnsafe) || result.Created || result.Registered {
			t.Fatalf("invalid creation role: %+v %v", result, e)
		}
	}
}
func TestPersistentCreatePreCancelled(t *testing.T) {
	touched := false
	dir, _, b := persistentMissing(t, persistentWAL, persistentHooks{beforeCreate: func(persistentRole) error { touched = true; return nil }})
	t.Cleanup(func() {
		if e := b.close(); e != nil {
			t.Error(e)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, e := b.createMissing(ctx, persistentWAL)
	if !errors.Is(e, context.Canceled) || result.Created || result.Registered || touched {
		t.Fatalf("pre-cancel effect: %+v %v touched=%v", result, e, touched)
	}
	if _, e = os.Stat(filepath.Join(dir, "store.db-wal")); !os.IsNotExist(e) {
		t.Fatalf("unexpected residual: %v", e)
	}
}
func TestPersistentCreateFaultResidual(t *testing.T) {
	for _, stage := range []string{"after-create", "after-register", "cancel-after-create"} {
		t.Run(stage, func(t *testing.T) {
			fault := errors.New("injected postcreate fault")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			called := false
			hooks := persistentHooks{}
			if stage == "after-register" {
				hooks.afterRegistered = func(role persistentRole) error {
					if role == persistentWAL {
						called = true
						return fault
					}
					return nil
				}
			} else {
				hooks.afterCreate = func(persistentRole) error {
					called = true
					if stage == "cancel-after-create" {
						cancel()
						return nil
					}
					return fault
				}
			}
			dir, r, b := persistentMissing(t, persistentWAL, hooks)
			expected := fault
			if stage == "cancel-after-create" {
				expected = context.Canceled
			}
			t.Cleanup(func() {
				if e := b.close(); !errors.Is(e, expected) {
					t.Errorf("terminal cleanup: %v", e)
				}
			})
			result, e := b.createMissing(ctx, persistentWAL)
			if !called || !errors.Is(e, expected) || !result.Created || result.Registered != (stage == "after-register") {
				t.Fatalf("postcreate fault: %+v %v called=%v", result, e, called)
			}
			if !b.evidence().Closed {
				t.Fatal("postcreate failure did not revoke admission")
			}
			if data, e := b.readWorkspace(context.Background(), r, []string{"neighbor.txt"}); data != nil || !errors.Is(e, errPersistentClosed) {
				t.Fatalf("fault allowed read: %v", e)
			}
			if e = b.close(); !errors.Is(e, expected) {
				t.Fatalf("lost failure: %v", e)
			}
			if v, e := os.ReadFile(filepath.Join(dir, "store.db-wal")); e != nil || len(v) != 0 {
				t.Fatalf("residual removed/changed: %v", e)
			}
		})
	}
}
func TestPersistentCreateUncertainCloseResidual(t *testing.T) {
	fault := errors.New("close outcome uncertain")
	calls := map[windows.Handle]int{}
	dir, r, b := persistentMissing(t, persistentSHM, persistentHooks{close: func(h windows.Handle) error { calls[h]++; return errors.Join(windows.CloseHandle(h), fault) }})
	t.Cleanup(func() {
		if e := b.close(); e != nil && !errors.Is(e, fault) {
			t.Error(e)
		}
	})
	result, e := b.createMissing(context.Background(), persistentSHM)
	if e != nil || !result.Created || !result.Registered {
		t.Fatalf("create: %+v %v", result, e)
	}
	if e = b.close(); !errors.Is(e, fault) {
		t.Fatalf("close: %v", e)
	}
	if e = b.close(); !errors.Is(e, fault) {
		t.Fatalf("repeat close: %v", e)
	}
	for _, n := range calls {
		if n != 1 {
			t.Fatal("uncertain close retried")
		}
	}
	if data, e := b.readWorkspace(context.Background(), r, []string{"neighbor.txt"}); data != nil || !errors.Is(e, errPersistentClosed) {
		t.Fatalf("poison released reads: %v", e)
	}
	if v, e := os.ReadFile(filepath.Join(dir, "store.db-shm")); e != nil || len(v) != 0 {
		t.Fatalf("uncertain close removed residual: %v", e)
	}
}
