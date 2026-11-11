//go:build windows

package rootlease

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"agent/internal/nativefs/windowsprobe"
	"golang.org/x/sys/windows"
)

// This adapter exists only in this test binary. It is not DB exclusion proof,
// production admission, or evidence of ordinary-user token qualification.
// In particular it does not replace or bypass the restricted-token test.
type nativeFixtureBackend struct {
	root          *windowsprobe.Root
	reads, closes atomic.Int32
}

func (b *nativeFixtureBackend) read(ctx context.Context, parts []string) ([]byte, error) {
	b.reads.Add(1)
	return b.root.Read(ctx, parts)
}
func (b *nativeFixtureBackend) close() error {
	b.closes.Add(1)
	return b.root.Close()
}

func nativeLongFixturePath(t *testing.T) string {
	t.Helper()
	short, err := windows.UTF16PtrFromString(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var buf [32768]uint16
	n, err := windows.GetLongPathName(short, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || n >= uint32(len(buf)) {
		t.Fatalf("fixture GetLongPathName: length=%d err=%v", n, err)
	}
	// Normalize only this fixture. The production native constructor receives
	// this exact path and retains all its rejection/identity checks unchanged.
	return windows.UTF16ToString(buf[:n])
}

func TestNativeRegistryCurrentTokenLifecycle(t *testing.T) {
	t.Log("disconnected lifecycle experiment using current Windows token; no ordinary-user or DB exclusion qualification")
	path := nativeLongFixturePath(t)
	if err := os.WriteFile(filepath.Join(path, "file"), []byte("native fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	var opens atomic.Int32
	var native *nativeFixtureBackend
	open := func(ctx context.Context, requested string) (backend, error) {
		opens.Add(1)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		root, err := windowsprobe.OpenLocalRoot(requested)
		if err != nil {
			return nil, err
		}
		native = &nativeFixtureBackend{root: root}
		return native, nil
	}
	r := fixtureRegistry(t, 1, open)
	p, err := r.acquire(context.Background(), path)
	if err != nil || p == nil {
		t.Fatalf("current-token native acquisition: %v", err)
	}
	requireRef(t, p.reference())
	b := binding{reference: p.reference(), path: path, scope: []byte("fixture-only scope"), policy: []byte("fixture-only policy; not DB proof")}
	requireRefusal(t, r, b)
	if opens.Load() != 1 || native == nil || native.reads.Load() != 0 {
		t.Fatal("prepared reference opened again or reached native Read")
	}
	// Test-only activation deliberately supplies no DB proof; production has no
	// exported constructor/admission path through which this could authorize IO.
	if err := p.activate(b); err != nil {
		t.Fatal(err)
	}
	got, err := r.read(context.Background(), b, []string{"file"})
	if err != nil || string(got) != "native fixture" || native.reads.Load() != 1 || opens.Load() != 1 {
		t.Fatalf("active native read: %q %v", got, err)
	}
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	if native.closes.Load() != 1 {
		t.Fatalf("native close count=%d", native.closes.Load())
	}
	requireRefusal(t, r, b)
	if native.reads.Load() != 1 || opens.Load() != 1 {
		t.Fatal("closed reference reopened or reached native Read")
	}
	// Verify actual underlying closure, not merely the wrapper call count.
	if got, err := native.root.Read(context.Background(), []string{"file"}); err == nil || got != nil {
		t.Fatalf("native root survived registry close: %q %v", got, err)
	}
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	if native.closes.Load() != 1 {
		t.Fatal("repeated registry close closed backend again")
	}
	fresh := fixtureRegistry(t, 1, open)
	requireRefusal(t, fresh, b)
	if opens.Load() != 1 || native.reads.Load() != 1 {
		t.Fatal("new registry revived old reference through opener or Read")
	}
	if err := fresh.close(); err != nil {
		t.Fatal(err)
	}
}
