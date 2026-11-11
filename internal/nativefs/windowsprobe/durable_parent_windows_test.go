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

// TestNativeDurableParentQualification measures existing OS behavior, not a
// production feature RED/GREEN. Expectations come from NtFlushBuffersFileEx's
// write/append requirement and Windows' bidirectional sharing contract.
// This runs under the current token; it is NOT reduced/standard-user evidence.
// No reusable guard policy is changed. Reparse-mutation qualification remains a
// prerequisite to any later directory SHARE_WRITE policy change.
func TestNativeDurableParentQualification(t *testing.T) {
	for _, shareWrite := range []bool{false, true} {
		for _, right := range []struct {
			name string
			mask uint32
		}{
			{"add-file", windows.FILE_WRITE_DATA},
			{"add-subdirectory", windows.FILE_APPEND_DATA},
		} {
			name := "legacy/" + right.name
			if shareWrite {
				name = "share-write/" + right.name
			}
			t.Run(name, func(t *testing.T) {
				dir, root := fixture(t)
				parent := filepath.Join(dir, "parent")
				if err := os.Mkdir(parent, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(parent, "sentinel"), []byte("unchanged"), 0600); err != nil {
					t.Fatal(err)
				}
				// Registered before handle cleanups, so verification runs after closes.
				t.Cleanup(func() {
					entries, err := os.ReadDir(dir)
					if err != nil || len(entries) != 1 || entries[0].Name() != "parent" || !entries[0].IsDir() {
						t.Errorf("outer namespace changed: %v %v", entries, err)
						return
					}
					entries, err = os.ReadDir(parent)
					if err != nil || len(entries) != 1 || entries[0].Name() != "sentinel" || entries[0].IsDir() {
						t.Errorf("inner namespace changed: %v %v", entries, err)
						return
					}
					data, err := os.ReadFile(filepath.Join(parent, "sentinel"))
					if err != nil || string(data) != "unchanged" {
						t.Errorf("content changed: %q %v", data, err)
					}
				})
				share := uint32(windows.FILE_SHARE_READ)
				if shareWrite {
					share |= windows.FILE_SHARE_WRITE
				}
				guard, err := durableParentOpen(root.h, windows.FILE_LIST_DIRECTORY, share)
				if err != nil {
					t.Fatal(err)
				}
				durableParentClose(t, guard)
				before, err := inspect(guard)
				if err != nil || before.Standard.Directory == 0 {
					t.Fatalf("guard inspection: %v", err)
				}
				if err := exactName(guard, "parent"); err != nil {
					t.Fatal(err)
				}
				// A list-only participating handle has neither write nor append access.
				status, iosb := durableParentFlush(t, guard)
				t.Logf("current-token guard share=%#x no-write flush status=%#x iosb=%#x info=%d", share, uint32(status), uint32(iosb.Status), iosb.Information)
				if status != windows.STATUS_ACCESS_DENIED {
					t.Fatalf("no-write flush: expected ACCESS_DENIED, got %#x", uint32(status))
				}
				writer, err := durableParentOpen(root.h, right.mask, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
				if !shareWrite {
					if writer != 0 {
						durableParentClose(t, writer)
					}
					if !errors.Is(err, windows.STATUS_SHARING_VIOLATION) {
						t.Fatalf("legacy sharing: expected SHARING_VIOLATION, handle=%v error=%v", writer, err)
					}
				} else {
					if err != nil {
						t.Fatalf("narrow writable open: %v", err)
					}
					durableParentClose(t, writer)
					after, err := inspect(writer)
					if err != nil || after.Standard.Directory == 0 || after.ID != before.ID {
						t.Fatalf("writable identity mismatch: %v", err)
					}
					var access uint32
					var io windows.IO_STATUS_BLOCK
					err = windows.NtQueryInformationFile(writer, &io, (*byte)(unsafe.Pointer(&access)), 4, 8)
					expected := right.mask | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE
					if err != nil || io.Status != 0 || io.Information != 4 || access != expected {
						t.Fatalf("granted access=%#x expected=%#x iosb=%+v error=%v", access, expected, io, err)
					}
					status, iosb = durableParentFlush(t, writer)
					t.Logf("current-token NTFS access=%#x share=%#x flush status=%#x iosb=%#x info=%d", access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, uint32(status), uint32(iosb.Status), iosb.Information)
					if status != 0 || iosb.Status != 0 {
						t.Fatalf("normal flush not qualified: status=%#x iosb=%#x", uint32(status), uint32(iosb.Status))
					}
				}
				// No SHARE_DELETE is granted by either case. This controlled negative
				// operation must not rename; cleanup verifies the original namespace.
				if err := os.Rename(parent, filepath.Join(dir, "moved")); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
					t.Fatalf("conventional rename must fail sharing: %v", err)
				}
			})
		}
	}
}

// Test-only exact relative OPEN; never creates or follows a reparse target.
func durableParentOpen(parent windows.Handle, right, share uint32) (windows.Handle, error) {
	name, err := windows.NewNTUnicodeString("parent")
	if err != nil {
		return 0, err
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: parent, ObjectName: name}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var h windows.Handle
	var io windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&h, right|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, &oa, &io, nil, 0, share, windows.FILE_OPEN, windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_NO_RECALL|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	return h, err
}
func durableParentFlush(t *testing.T, h windows.Handle) (windows.NTStatus, windows.IO_STATUS_BLOCK) {
	t.Helper()
	proc := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtFlushBuffersFileEx")
	if err := proc.Find(); err != nil {
		t.Fatal(err)
	}
	var io windows.IO_STATUS_BLOCK
	status, _, _ := proc.Call(uintptr(h), 0, 0, 0, uintptr(unsafe.Pointer(&io)))
	return windows.NTStatus(status), io
}
func durableParentClose(t *testing.T, h windows.Handle) {
	t.Helper()
	t.Cleanup(func() {
		if err := windows.CloseHandle(h); err != nil {
			t.Errorf("close qualification handle: %v", err)
		}
	})
}
