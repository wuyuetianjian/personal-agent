//go:build windows && nativeprobe_sqlite_identity

package windowsprobe

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func protectedFixtureFile(t *testing.T, path string, content string) windows.Handle {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := windows.CloseHandle(h); err != nil {
			t.Error(err)
		}
	})
	return h
}

func TestNativeProtectedObjectBeforeIOOpen(t *testing.T) {
	dir, r := fixture(t)
	h := protectedFixtureFile(t, filepath.Join(dir, "notes.txt"), "private database-like content")
	if got, err := r.Read(context.Background(), []string{"notes.txt"}); err == nil || got != nil {
		t.Fatalf("writer sharing positive control: %q %v", got, err)
	}
	got, err := r.ExperimentalReadExcluding(context.Background(), []string{"notes.txt"}, []windows.Handle{h})
	if !errors.Is(err, ErrProtected) || got != nil {
		t.Fatalf("must reject actual ID before I/O reopen, not sharing: %q %v", got, err)
	}
}

func TestNativeProtectedNeighborAndOwnership(t *testing.T) {
	dir, r := fixture(t)
	h := protectedFixtureFile(t, filepath.Join(dir, "notes.txt"), "private")
	if err := os.WriteFile(filepath.Join(dir, "public.txt"), []byte("public"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		got, err := r.ExperimentalReadExcluding(context.Background(), []string{"public.txt"}, []windows.Handle{h})
		if err != nil || string(got) != "public" {
			t.Fatalf("neighbor: %q %v", got, err)
		}
	}
	if _, err := inspect(h); err != nil {
		t.Fatalf("borrowed protected handle was closed: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := r.ExperimentalReadExcluding(context.Background(), []string{"public.txt"}, []windows.Handle{h}); err == nil || got != nil {
		t.Fatalf("closed root: %q %v", got, err)
	}
}

func TestNativeProtectedInputsFailClosed(t *testing.T) {
	dir, r := fixture(t)
	if err := os.WriteFile(filepath.Join(dir, "public.txt"), []byte("public"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, handles := range [][]windows.Handle{nil, {}, {0}, {windows.InvalidHandle}, {r.h}, make([]windows.Handle, 17)} {
		if got, err := r.ExperimentalReadExcluding(context.Background(), []string{"public.txt"}, handles); err == nil || got != nil {
			t.Fatalf("invalid protection returned bytes: %q %v", got, err)
		}
	}
}

func TestNativeProtectedPartialFailureReleasesDuplicates(t *testing.T) {
	dir, r := fixture(t)
	h := protectedFixtureFile(t, filepath.Join(dir, "notes.txt"), "private")
	if err := os.WriteFile(filepath.Join(dir, "public.txt"), []byte("public"), 0600); err != nil {
		t.Fatal(err)
	}
	before := captureHandleInventory(t)
	for i := 0; i < 30; i++ {
		if got, err := r.ExperimentalReadExcluding(context.Background(), []string{"public.txt"}, []windows.Handle{h, windows.InvalidHandle}); err == nil || got != nil {
			t.Fatalf("partial invalid protection: %q %v", got, err)
		}
	}
	after := captureHandleInventory(t)
	if !sameDiskHandles(before, after) {
		logHandleDelta(t, before, after)
		t.Fatal("partial protection acquisition leaked disk handles")
	}
	if _, err := inspect(h); err != nil {
		t.Fatalf("caller handle closed: %v", err)
	}
}
