//go:build windows

package windowsprobe

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
)

// Each hook closes the real owned handle before reporting an injected failure.
// This proves propagation and one-shot consumption, not an actual OS close failure.
type cleanupTrace struct {
	t              *testing.T
	live           map[windows.Handle]bool
	opened, closed []windows.Handle
	closeErr       error
	openErr        error
	failData       bool
}

func newCleanupTrace(t *testing.T) *cleanupTrace {
	c := &cleanupTrace{t: t, live: map[windows.Handle]bool{}}
	t.Cleanup(func() {
		// In the RED stage handle+error was abandoned before any close attempt.
		// Clean only those still-owned fixtures; never retry an attempted close.
		for h := range c.live {
			delete(c.live, h)
			if e := windows.CloseHandle(h); e != nil {
				t.Error(e)
			}
		}
	})
	return c
}
func (c *cleanupTrace) ops() handleOps {
	return handleOps{open: func(parent windows.Handle, name string, data bool, share uint32) (windows.Handle, error) {
		h, e := openRelativeShared(parent, name, data, share)
		if h != 0 && h != windows.InvalidHandle {
			c.live[h] = true
			c.opened = append(c.opened, h)
		}
		if e == nil && c.openErr != nil && data == c.failData {
			e = c.openErr
		}
		return h, e
	}, close: func(h windows.Handle) error {
		if !c.live[h] {
			c.t.Errorf("unowned/repeated close %x", h)
			return errors.New("unexpected close")
		}
		delete(c.live, h)
		c.closed = append(c.closed, h)
		return errors.Join(windows.CloseHandle(h), c.closeErr)
	}}
}
func cleanupFile(t *testing.T) (string, *Root) {
	t.Helper()
	dir, r := fixture(t)
	if e := os.WriteFile(filepath.Join(dir, "leaf"), []byte("bounded native contents"), 0600); e != nil {
		t.Fatal(e)
	}
	return dir, r
}
func TestReadCleanupSuccessReturnsNoBytesOnCloseError(t *testing.T) {
	dir, r := cleanupFile(t)
	if e := os.Mkdir(filepath.Join(dir, "nested"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "nested", "leaf"), []byte("nested native contents"), 0600); e != nil {
		t.Fatal(e)
	}
	baseline := captureHandleInventory(t)
	trace := newCleanupTrace(t)
	fault := errors.New("after-real-close uncertainty")
	trace.closeErr = fault
	data, e := r.readWithHandleOps(context.Background(), []string{"nested", "leaf"}, nil, trace.ops())
	if !errors.Is(e, fault) || data != nil {
		t.Errorf("cleanup error must discard contents: bytes=%d error=%v", len(data), e)
	}
	if len(trace.opened) != 4 || len(trace.closed) != 4 || len(trace.live) != 0 {
		t.Errorf("ownership opened=%d closed=%d live=%d", len(trace.opened), len(trace.closed), len(trace.live))
	}
	for i, h := range trace.closed {
		if h != trace.opened[len(trace.opened)-1-i] {
			t.Error("cleanup not reverse acquisition order")
		}
	}
	if after := captureHandleInventory(t); !reflect.DeepEqual(baseline.files, after.files) {
		t.Errorf("disk handle set changed")
	}
	// Failure applies to temporary ownership; caller-owned root was not revoked.
	if data, e := r.Read(context.Background(), []string{"leaf"}); e != nil || string(data) != "bounded native contents" {
		t.Fatalf("caller root changed: %q %v", data, e)
	}
}
func TestReadCleanupPreservesPrimaryFailure(t *testing.T) {
	_, r := cleanupFile(t)
	trace := newCleanupTrace(t)
	fault := errors.New("after-close failure")
	trace.closeErr = fault
	h, e := openRelative(r.h, "leaf", false)
	if e != nil {
		t.Fatal(e)
	}
	s, e := inspect(h)
	ce := windows.CloseHandle(h)
	if e != nil || ce != nil {
		t.Fatal(errors.Join(e, ce))
	}
	data, e := r.readWithHandleOps(context.Background(), []string{"leaf"}, map[idInfo]struct{}{s.ID: {}}, trace.ops())
	if data != nil || !errors.Is(e, errProtected) || !errors.Is(e, fault) {
		t.Fatalf("lost primary/cleanup: bytes=%d error=%v", len(data), e)
	}
	if len(trace.closed) != 1 || len(trace.live) != 0 {
		t.Fatalf("ownership closed=%d live=%d", len(trace.closed), len(trace.live))
	}
}
func TestReadCleanupOwnsMetadataHandleWithOpenError(t *testing.T) {
	_, r := cleanupFile(t)
	trace := newCleanupTrace(t)
	primary, cleanup := errors.New("open returned owned handle and error"), errors.New("after-close failure")
	trace.openErr, trace.closeErr = primary, cleanup
	data, e := r.readWithHandleOps(context.Background(), []string{"leaf"}, nil, trace.ops())
	if data != nil || !errors.Is(e, primary) || !errors.Is(e, cleanup) {
		t.Errorf("lost returned ownership/error: bytes=%d error=%v", len(data), e)
	}
	if len(trace.opened) != 1 || len(trace.closed) != 1 || len(trace.live) != 0 {
		t.Errorf("returned handle abandoned: opened=%d closed=%d live=%d", len(trace.opened), len(trace.closed), len(trace.live))
	}
}
func cleanupExpected(t *testing.T, r *Root) snapshot {
	t.Helper()
	h, e := openRelative(r.h, "leaf", false)
	if e != nil {
		t.Fatal(e)
	}
	s, e := inspect(h)
	ce := windows.CloseHandle(h)
	if e != nil || ce != nil {
		t.Fatal(errors.Join(e, ce))
	}
	return s
}
func TestGuardCleanupPreservesQualificationFailure(t *testing.T) {
	_, r := cleanupFile(t)
	expected := cleanupExpected(t, r)
	expected.ID.ID[0] ^= 1
	trace := newCleanupTrace(t)
	fault := errors.New("after-close failure")
	trace.closeErr = fault
	h, _, e := acquireIOGuardWithOps(r.h, "leaf", expected, trace.ops())
	if h != 0 || !errors.Is(e, errUnsafe) || !errors.Is(e, fault) {
		t.Errorf("lost qualification/cleanup: handle=%x error=%v", h, e)
	}
	if len(trace.closed) != 1 || len(trace.live) != 0 {
		t.Errorf("owned guard cleanup count=%d live=%d", len(trace.closed), len(trace.live))
	}
}
func TestGuardCleanupOwnsHandleWithOpenError(t *testing.T) {
	_, r := cleanupFile(t)
	expected := cleanupExpected(t, r)
	trace := newCleanupTrace(t)
	primary, cleanup := errors.New("open returned owned guard and error"), errors.New("after-close failure")
	trace.openErr, trace.closeErr, trace.failData = primary, cleanup, true
	h, _, e := acquireIOGuardWithOps(r.h, "leaf", expected, trace.ops())
	if h != 0 || !errors.Is(e, primary) || !errors.Is(e, cleanup) {
		t.Errorf("lost owned guard/error: handle=%x error=%v", h, e)
	}
	if len(trace.opened) != 1 || len(trace.closed) != 1 || len(trace.live) != 0 {
		t.Errorf("returned guard abandoned: opened=%d closed=%d live=%d", len(trace.opened), len(trace.closed), len(trace.live))
	}
}
