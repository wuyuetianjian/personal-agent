//go:build windows

package windowsprobe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

type entriesObserver interface {
	ObserveEntries(context.Context, []string) ([]Entry, error)
}

func observationAPI(t *testing.T, r *Root) entriesObserver {
	t.Helper()
	api, ok := any(r).(entriesObserver)
	if !ok {
		t.Fatal("Root.ObserveEntries(context.Context, []string) ([]Entry, error) is not implemented")
	}
	return api
}

func observationFixture(t *testing.T) (string, *Root, entriesObserver) {
	t.Helper()
	dir, r := fixture(t)
	api := observationAPI(t, r)
	if err := os.Mkdir(filepath.Join(dir, "Listed"), 0700); err != nil {
		t.Fatal(err)
	}
	return dir, r, api
}

func rejectObservation(t *testing.T, api entriesObserver, ctx context.Context, parts []string) {
	t.Helper()
	got, err := api.ObserveEntries(ctx, parts)
	if err == nil || got != nil {
		t.Fatalf("expected nil entries and error for %q: entries=%d err=%v", parts, len(got), err)
	}
}

func TestNativeObserveEntriesOrdinary(t *testing.T) {
	dir, _, api := observationFixture(t)
	got, err := api.ObserveEntries(context.Background(), []string{"Listed"})
	if err != nil || len(got) != 0 {
		t.Fatalf("empty directory: %v %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Listed", "普通😀.txt"), []byte("not returned"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "Listed", "Folder"), 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		got, err = api.ObserveEntries(context.Background(), []string{"Listed"})
		if err != nil || len(got) != 2 {
			t.Fatalf("ordinary entries: %d %v", len(got), err)
		}
		seen := map[string]bool{}
		for _, e := range got {
			if seen[e.Name] {
				t.Fatalf("duplicate name %q", e.Name)
			}
			seen[e.Name] = true
			if e.Name != "普通😀.txt" && e.Name != "Folder" {
				t.Fatalf("unexpected name %q", e.Name)
			}
			if e.ObservedDirectory != (e.Name == "Folder") {
				t.Fatalf("wrong directory observation: %+v", e)
			}
			// Compare only to a current metadata observation, not a durable identity promise.
			p, err := windows.UTF16PtrFromString(filepath.Join(dir, "Listed", e.Name))
			if err != nil {
				t.Fatal(err)
			}
			h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
			if err != nil {
				t.Fatal(err)
			}
			var id idInfo
			err = query(h, windows.FileIdInfo, &id)
			windows.CloseHandle(h)
			if err != nil || e.ObservedID != id.ID {
				t.Fatalf("observed ID mismatch: %v", err)
			}
		}
	}
}

func TestNativeObserveEntriesMultiPageAndIndependentCursors(t *testing.T) {
	dir, r, api := observationFixture(t)
	names := map[string]bool{}
	for i := 0; i < 160; i++ {
		name := fmt.Sprintf("%03d-%s", i, strings.Repeat("x", 220))
		names[name] = true
		if err := os.WriteFile(filepath.Join(dir, "Listed", name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// 160 records with >440 bytes of name each necessarily exceed one 64 KiB buffer.
	clone, err := NewRoot(r.h)
	if err != nil {
		t.Fatal(err)
	}
	defer clone.Close()
	other := observationAPI(t, clone)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, observer := range []entriesObserver{api, other} {
		wg.Add(1)
		go func(observer entriesObserver) {
			defer wg.Done()
			for round := 0; round < 4; round++ {
				got, err := observer.ObserveEntries(context.Background(), []string{"Listed"})
				if err != nil || len(got) != len(names) {
					errs <- fmt.Errorf("multi-page count=%d err=%v", len(got), err)
					return
				}
				seen := map[string]bool{}
				for _, e := range got {
					if !names[e.Name] || seen[e.Name] {
						errs <- fmt.Errorf("unexpected/duplicate name %q", e.Name)
						return
					}
					seen[e.Name] = true
				}
			}
		}(observer)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestNativeObserveEntriesDoesNotOpenContent(t *testing.T) {
	dir, _, api := observationFixture(t)
	path := filepath.Join(dir, "Listed", "file")
	if err := os.WriteFile(path, []byte("private content"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	got, err := api.ObserveEntries(context.Background(), []string{"Listed"})
	if err != nil || len(got) != 1 || got[0].Name != "file" {
		t.Fatalf("exclusive content handle blocked metadata observation: %v %v", got, err)
	}
}

func TestNativeObserveEntriesRejectsReparseAndDangerousNames(t *testing.T) {
	for _, kind := range []string{"symlink", "junction", "protected"} {
		t.Run(kind, func(t *testing.T) {
			dir, _, api := observationFixture(t)
			if err := os.WriteFile(filepath.Join(dir, "Listed", "ordinary"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			target := t.TempDir()
			switch kind {
			case "symlink":
				if err := os.Symlink(target, filepath.Join(dir, "Listed", "Link")); err != nil {
					t.Fatalf("required symlink fixture: %v", err)
				}
			case "junction":
				if out, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", filepath.Join(dir, "Listed", "Link"), target).CombinedOutput(); err != nil {
					t.Fatalf("required junction fixture: %v %s", err, out)
				}
			case "protected":
				if err := os.Mkdir(filepath.Join(dir, "Listed", "data"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			rejectObservation(t, api, context.Background(), []string{"Listed"})
		})
	}
}

func TestNativeObserveEntriesLimitNoPartial(t *testing.T) {
	dir, _, api := observationFixture(t)
	for i := 0; i < 256; i++ {
		if err := os.WriteFile(filepath.Join(dir, "Listed", fmt.Sprintf("file%03d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := api.ObserveEntries(context.Background(), []string{"Listed"})
	if err != nil || len(got) != 256 {
		t.Fatalf("exact limit: %d %v", len(got), err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Listed", "extra"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	rejectObservation(t, api, context.Background(), []string{"Listed"})
}

func TestNativeObserveEntriesCancelledClosedAndPaths(t *testing.T) {
	dir, r, api := observationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := api.ObserveEntries(ctx, []string{"Listed"})
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("cancelled: %d %v", len(got), err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, parts := range [][]string{nil, {}, {"."}, {".."}, {"listed"}, {"Listed", ".."}, {"Listed:stream"}, {filepath.Join(dir, "Listed")}, {"missing"}, {"file"}} {
		rejectObservation(t, api, context.Background(), parts)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	rejectObservation(t, api, context.Background(), []string{"Listed"})
}

func TestNativeObserveEntriesDerivedRootSurvivesParentClose(t *testing.T) {
	dir, r, _ := observationFixture(t)
	if err := os.Mkdir(filepath.Join(dir, "Listed", "Nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Listed", "Nested", "file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	child := deriveDirectory(t, r, "Listed")
	api := observationAPI(t, child)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := api.ObserveEntries(context.Background(), []string{"Nested"})
	if err != nil || len(got) != 1 || got[0].Name != "file" {
		t.Fatalf("derived observation: %v %v", got, err)
	}
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "Listed"), filepath.Join(dir, "Moved")); err != nil {
		t.Fatalf("retained scan handle: %v", err)
	}
}

func TestNativeObserveEntriesHandleRelease(t *testing.T) {
	dir, _, api := observationFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "Listed", "file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "Bad", "data"), 0700); err != nil {
		t.Fatal(err)
	}
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")
	if err := proc.Find(); err != nil {
		t.Fatal(err)
	}
	count := func() uint32 {
		var n uint32
		ok, _, err := proc.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&n)))
		if ok == 0 {
			t.Fatalf("GetProcessHandleCount: %v", err)
		}
		return n
	}
	exercise := func() {
		got, err := api.ObserveEntries(context.Background(), []string{"Listed"})
		if err != nil || len(got) != 1 {
			t.Fatalf("observation: %v %v", got, err)
		}
		rejectObservation(t, api, context.Background(), []string{"Listed", "missing"})
		rejectObservation(t, api, context.Background(), []string{"Bad"})
	}
	exercise()
	count()
	before := count()
	for i := 0; i < 64; i++ {
		exercise()
	}
	if after := count(); after != before {
		t.Fatalf("observation leaked handles: before=%d after=%d", before, after)
	}
	for _, name := range []string{"Listed", "Bad"} {
		if err := os.Rename(filepath.Join(dir, name), filepath.Join(dir, name+"Moved")); err != nil {
			t.Fatalf("retained scan guard: %v", err)
		}
	}
}
