//go:build windows

package windowsprobe

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Current-token NTFS contract only. These tests neither enable production
// writes nor establish standard-user, DB exclusion, or universal reparse safety.
func actualDirectoryGuard(t *testing.T) (string, *Root, windows.Handle) {
	t.Helper()
	dir, root := fixture(t)
	if err := os.Mkdir(filepath.Join(dir, "parent"), 0700); err != nil {
		t.Fatal(err)
	}
	meta, err := openRelative(root.h, "parent", false)
	if err != nil {
		t.Fatal(err)
	}
	before, inspectErr := inspect(meta)
	closeErr := windows.CloseHandle(meta)
	if inspectErr != nil || closeErr != nil {
		t.Fatalf("metadata fixture: inspect=%v close=%v", inspectErr, closeErr)
	}
	guard, _, err := acquireIOGuard(root.h, "parent", before)
	if err != nil {
		t.Fatal(err)
	}
	return dir, root, guard
}

func TestNativeDirectoryGuardPermitsNarrowParentFlush(t *testing.T) {
	for _, right := range []struct {
		name string
		mask uint32
	}{{"add-file", windows.FILE_WRITE_DATA}, {"append-add-subdirectory", windows.FILE_APPEND_DATA}} {
		t.Run(right.name, func(t *testing.T) {
			dir, root, guard := actualDirectoryGuard(t)
			durableParentClose(t, guard)
			before, err := inspect(guard)
			if err != nil {
				t.Fatal(err)
			}
			writer, err := durableParentOpen(root.h, right.mask, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
			if writer != 0 {
				durableParentClose(t, writer)
			}
			if err != nil || writer == 0 {
				t.Fatalf("actual directory IO guard must allow narrow writer, not legacy READ-only sharing: handle=%v error=%v", writer, err)
			}
			after, err := inspect(writer)
			if err != nil || !sameDirectoryGuard(before, after) {
				t.Fatalf("narrow writer identity mismatch: %v", err)
			}
			var access uint32
			var io windows.IO_STATUS_BLOCK
			err = windows.NtQueryInformationFile(writer, &io, (*byte)(unsafe.Pointer(&access)), 4, 8)
			expected := right.mask | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE
			if err != nil || io.Status != 0 || io.Information != 4 || access != expected {
				t.Fatalf("narrow writer access=%#x expected=%#x error=%v", access, expected, err)
			}
			status, iosb := durableParentFlush(t, writer)
			if status != 0 || iosb.Status != 0 {
				t.Fatalf("normal directory flush: status=%#x iosb=%#x", uint32(status), uint32(iosb.Status))
			}
			entries, err := os.ReadDir(filepath.Join(dir, "parent"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("open/flush unexpectedly changed directory: count=%d error=%v", len(entries), err)
			}
		})
	}
}

func TestNativeFileGuardStillRefusesOrdinaryWriter(t *testing.T) {
	dir, root := fixture(t)
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	meta, err := openRelative(root.h, "file", false)
	if err != nil {
		t.Fatal(err)
	}
	before, inspectErr := inspect(meta)
	closeErr := windows.CloseHandle(meta)
	if inspectErr != nil || closeErr != nil {
		t.Fatalf("file metadata: %v %v", inspectErr, closeErr)
	}
	guard, _, err := acquireIOGuard(root.h, "file", before)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := windows.CloseHandle(guard); err != nil {
			t.Error(err)
		}
	}()
	writer, err := os.OpenFile(path, os.O_WRONLY, 0)
	if writer != nil {
		writer.Close()
		t.Fatal("file IO guard admitted ordinary writer")
	}
	if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("file writer must fail sharing: %v", err)
	}
	after, err := inspect(guard)
	if err != nil || !unchanged(before, after) {
		t.Fatalf("file guard metadata changed: %v", err)
	}
}

func TestNativeDirectoryGuardStillRefusesRenameAndDeleteAccess(t *testing.T) {
	dir, root, guard := actualDirectoryGuard(t)
	open := true
	defer func() {
		if open {
			windows.CloseHandle(guard)
		}
	}()
	deleting, err := durableParentOpen(root.h, windows.DELETE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
	if deleting != 0 {
		windows.CloseHandle(deleting)
		t.Fatal("directory guard admitted DELETE access")
	}
	if !errors.Is(err, windows.STATUS_SHARING_VIOLATION) {
		t.Fatalf("DELETE access must fail sharing: %v", err)
	}
	parent := filepath.Join(dir, "parent")
	if err := os.Rename(parent, filepath.Join(dir, "moved")); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("guarded conventional rename: %v", err)
	}
	if err := windows.CloseHandle(guard); err != nil {
		t.Fatal(err)
	}
	open = false
	if err := os.Rename(parent, filepath.Join(dir, "moved")); err != nil {
		t.Fatalf("closed directory guard retained sharing lock: %v", err)
	}
}

func TestNativeDirectoryShareWriteDerivedLineageOwnership(t *testing.T) {
	dir, root := fixture(t)
	parentPath := filepath.Join(dir, "parent")
	if err := os.MkdirAll(filepath.Join(parentPath, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	parent := deriveDirectory(t, root, "parent")
	child := deriveDirectory(t, parent, "child")
	writer, err := durableParentOpen(root.h, windows.FILE_WRITE_DATA, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
	if err != nil || writer == 0 {
		if writer != 0 {
			windows.CloseHandle(writer)
		}
		t.Fatalf("derived ancestor guards must allow narrow writer: %v", err)
	}
	if err := windows.CloseHandle(writer); err != nil {
		t.Fatal(err)
	}
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{parentPath, filepath.Join(parentPath, "child")} {
		if err := os.Rename(path, path+"Moved"); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			t.Fatalf("independent descendant lost lineage guard: %v", err)
		}
	}
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(parentPath, filepath.Join(dir, "moved")); err != nil {
		t.Fatalf("closed derived lineage retained guard: %v", err)
	}
}
