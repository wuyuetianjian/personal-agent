//go:build darwin && cgo && nativeprobe_darwin_bulk

package darwinprobe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"golang.org/x/sys/unix"
)

func bulkFixture(t *testing.T) (string, *DirectoryObservation) {
	t.Helper()
	p, f := rootFixture(t)
	d, e := ObserveDirectory(f, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := d.Close(); e != nil {
			t.Error(e)
		}
	})
	return p, d
}
func bulkEmptyOnError(t *testing.T, got BulkObservation, err error) {
	t.Helper()
	if errors.Is(err, errBulkNotImplemented) {
		t.Fatalf("native behavior remains unimplemented: %v", err)
	}
	if err == nil || len(got.Entries) != 0 || got.Calls != 0 || got.RawRecords != 0 {
		t.Fatalf("expected error without partial result: %+v %v", got, err)
	}
}
func TestBulkNativeNamesAndIndependentCursor(t *testing.T) {
	p, d := bulkFixture(t)
	if e := os.WriteFile(filepath.Join(p, "leaf"), []byte("not read"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(filepath.Join(p, "folder"), 0700); e != nil {
		t.Fatal(e)
	}
	var st unix.Stat_t
	if e := unix.Lstat(filepath.Join(p, "leaf"), &st); e != nil {
		t.Fatal(e)
	}
	var previous map[string]BulkEntry
	for round := 0; round < 2; round++ {
		got, e := d.ExperimentalObserveEntries(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		if got.Calls < 1 || got.Calls > 16 || got.RawRecords < 2 || got.RawRecords > 1024 || len(got.Entries) != 2 {
			t.Fatalf("bad evidence: %+v", got)
		}
		byName := map[string]BulkEntry{}
		for _, entry := range got.Entries {
			byName[entry.Name] = entry
		}
		if byName["leaf"].Inode != st.Ino || byName["leaf"].Directory || !byName["folder"].Directory {
			t.Fatalf("bad metadata: %+v", got)
		}
		if previous != nil && !reflect.DeepEqual(previous, byName) {
			t.Fatalf("shared scan cursor: %+v -> %+v", previous, byName)
		}
		previous = byName
	}
}
func TestBulkNativeUnsafeEntriesNoFIFOReader(t *testing.T) {
	for _, kind := range []string{"fifo", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			p, d := bulkFixture(t)
			leaf := filepath.Join(p, "unsafe")
			if kind == "fifo" {
				if e := unix.Mkfifo(leaf, 0600); e != nil {
					t.Fatal(e)
				}
			} else {
				if e := os.Symlink("/dev/null", leaf); e != nil {
					t.Fatal(e)
				}
			}
			noReader := func() {
				t.Helper()
				fd, e := unix.Open(leaf, unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
				if fd >= 0 {
					unix.Close(fd)
				}
				if !errors.Is(e, unix.ENXIO) {
					t.Fatalf("FIFO writer unexpected result fd=%d err=%v", fd, e)
				}
			}
			if kind == "fifo" {
				noReader()
			}
			got, e := d.ExperimentalObserveEntries(context.Background())
			bulkEmptyOnError(t, got, e)
			if kind == "fifo" {
				noReader()
			}
		})
	}
}
func TestBulkNativeLimitNoPartial(t *testing.T) {
	p, d := bulkFixture(t)
	for i := 0; i < 257; i++ {
		if e := os.WriteFile(filepath.Join(p, fmt.Sprintf("f%03d", i)), nil, 0600); e != nil {
			t.Fatal(e)
		}
	}
	got, e := d.ExperimentalObserveEntries(context.Background())
	bulkEmptyOnError(t, got, e)
}
func TestBulkNativeClosedCancelled(t *testing.T) {
	var zero DirectoryObservation
	got, e := zero.ExperimentalObserveEntries(context.Background())
	bulkEmptyOnError(t, got, e)
	if !errors.Is(e, os.ErrClosed) {
		t.Fatalf("zero observation: %v", e)
	}
	_, d := bulkFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, e = d.ExperimentalObserveEntries(ctx)
	bulkEmptyOnError(t, got, e)
	if !errors.Is(e, context.Canceled) {
		t.Fatalf("cancellation: %v", e)
	}
}
func TestBulkNativeDescriptorOwnership(t *testing.T) {
	p, d := bulkFixture(t)
	if e := os.WriteFile(filepath.Join(p, "f"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	before := openDescriptorSnapshot(t)
	for i := 0; i < 8; i++ {
		if _, e := d.ExperimentalObserveEntries(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	after := openDescriptorSnapshot(t)
	if !slices.Equal(before, after) {
		t.Fatalf("descriptor set changed: %v -> %v", before, after)
	}
}
