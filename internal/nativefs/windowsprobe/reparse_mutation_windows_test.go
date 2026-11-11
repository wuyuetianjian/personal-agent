//go:build windows

package windowsprobe

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// OS qualification only: no reusable sharing policy changes. A failed attack
// setup is not evidence that held-handle traversal is safe. Expectations derive
// from MS-FSA 2.1.5.10.37 (WRITE_DATA or WRITE_ATTRIBUTES, empty directory).
func TestNativeReparseMutationQualification(t *testing.T) {
	cases := []struct {
		name         string
		guard        bool
		share, right uint32
	}{
		{"unguarded-positive", false, 0, windows.FILE_WRITE_DATA | windows.FILE_WRITE_ATTRIBUTES},
		{"legacy-data", true, windows.FILE_SHARE_READ, windows.FILE_WRITE_DATA},
		{"legacy-attributes", true, windows.FILE_SHARE_READ, windows.FILE_WRITE_ATTRIBUTES},
		{"share-write-data", true, windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE, windows.FILE_WRITE_DATA},
		{"share-write-attributes", true, windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE, windows.FILE_WRITE_ATTRIBUTES},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Deliberately not t.TempDir: cleanup never recursively traverses a junction,
			// including when FSCTL_DELETE_REPARSE_POINT fails.
			base, err := os.MkdirTemp("", "agent-reparse-work-")
			if err != nil {
				t.Fatal(err)
			}
			outside := ""
			victim := filepath.Join(base, "victim")
			var handles []windows.Handle
			var attacker windows.Handle
			tagged := false
			t.Cleanup(func() {
				clean := true
				if tagged {
					b := make([]byte, 8)
					binary.LittleEndian.PutUint32(b, windows.IO_REPARSE_TAG_MOUNT_POINT)
					var returned uint32
					if err := windows.DeviceIoControl(attacker, windows.FSCTL_DELETE_REPARSE_POINT, &b[0], uint32(len(b)), nil, 0, &returned, nil); err != nil {
						t.Errorf("delete reparse failed; leaving workspace without recursive cleanup: %v", err)
						clean = false
					}
				}
				for i := len(handles) - 1; i >= 0; i-- {
					if err := windows.CloseHandle(handles[i]); err != nil {
						t.Errorf("close: %v", err)
					}
				}
				if outside != "" {
					entries, err := os.ReadDir(outside)
					if err != nil || len(entries) != 1 || entries[0].Name() != "sentinel" {
						t.Errorf("outside namespace changed: %v %v", entries, err)
					}
					b, err := os.ReadFile(filepath.Join(outside, "sentinel"))
					if err != nil || string(b) != "outside-do-not-read-through-root" {
						t.Errorf("outside content changed: %q %v", b, err)
					}
					for _, p := range []string{filepath.Join(outside, "sentinel"), outside} {
						if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
							t.Errorf("nonrecursive cleanup: %v", err)
						}
					}
				}
				if clean {
					for _, p := range []string{victim, base} {
						if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
							t.Errorf("nonrecursive cleanup: %v", err)
						}
					}
				}
			})
			outside, err = os.MkdirTemp("", "agent-reparse-outside-")
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Mkdir(victim, 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(outside, "sentinel"), []byte("outside-do-not-read-through-root"), 0600); err != nil {
				t.Fatal(err)
			}
			openPath := func(path string, access uint32) windows.Handle {
				p, err := windows.UTF16PtrFromString(path)
				if err != nil {
					t.Fatal(err)
				}
				h, err := windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
				if err != nil {
					t.Fatal(err)
				}
				handles = append(handles, h)
				return h
			}
			parent := openPath(base, windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE)
			if s, err := inspect(parent); err != nil || s.Standard.Directory == 0 {
				t.Fatalf("NTFS parent: %v", err)
			}
			sentinel := openPath(filepath.Join(outside, "sentinel"), windows.FILE_READ_ATTRIBUTES)
			var outsideID idInfo
			if err := query(sentinel, windows.FileIdInfo, &outsideID); err != nil {
				t.Fatal(err)
			}
			var guard windows.Handle
			var before snapshot
			if tc.guard {
				guard, err = reparseMutationOpen(parent, windows.FILE_LIST_DIRECTORY, tc.share)
				if err != nil {
					t.Fatal(err)
				}
				handles = append(handles, guard)
				before, err = inspect(guard)
				if err != nil || before.Standard.Directory == 0 {
					t.Fatalf("guard inspect: %v", err)
				}
			}
			attacker, err = reparseMutationOpen(parent, tc.right, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
			if attacker != 0 {
				handles = append(handles, attacker)
			}
			if tc.name == "legacy-data" {
				if !errors.Is(err, windows.STATUS_SHARING_VIOLATION) {
					t.Fatalf("expected sharing refusal: %v", err)
				}
				t.Log("attack open refused by legacy sharing; traversal after mutation NOT exercised")
				return
			}
			if err != nil {
				t.Fatalf("attack setup failed; not a traversal safety pass: %v", err)
			}
			s, err := inspect(attacker)
			if err != nil || s.Standard.Directory == 0 || (tc.guard && s.ID != before.ID) {
				t.Fatalf("attacker identity: %v", err)
			}
			payload := reparseMutationBuffer(t, outside)
			var returned uint32
			if err := windows.DeviceIoControl(attacker, windows.FSCTL_SET_REPARSE_POINT, &payload[0], uint32(len(payload)), nil, 0, &returned, nil); err != nil {
				t.Fatalf("SET failed, attack condition not established: %v", err)
			}
			tagged = true
			// A successful SET can store a nonexistent target. Establish that this
			// exact junction really resolves to our outside sentinel before treating
			// a held-root relative NAME_NOT_FOUND as a meaningful contrast.
			control := openPath(filepath.Join(victim, "sentinel"), windows.FILE_READ_ATTRIBUTES)
			var controlID idInfo
			if err := query(control, windows.FileIdInfo, &controlID); err != nil || controlID != outsideID {
				t.Fatalf("junction target identity positive control: %v", err)
			}
			t.Logf("current-token SET succeeded: requested-right=%#x guard-share=%#x", tc.right, tc.share)
			if !tc.guard {
				return
			}
			var id idInfo
			var tag tagInfo
			if err := query(guard, windows.FileIdInfo, &id); err != nil {
				t.Fatal(err)
			}
			if err := query(guard, windows.FileAttributeTagInfo, &tag); err != nil {
				t.Fatal(err)
			}
			if id != before.ID || tag.Tag != windows.IO_REPARSE_TAG_MOUNT_POINT || tag.Attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
				t.Fatal("held guard did not observe same-object mount-point mutation")
			}
			if _, err := inspect(guard); !errors.Is(err, errUnsafe) {
				t.Fatalf("inspect must reject mutated guard: %v", err)
			}
			// Test-only borrowed handle, never Close: centralized cleanup owns it.
			root := &Root{h: guard, id: before.ID}
			if data, err := root.Read(context.Background(), []string{"sentinel"}); !errors.Is(err, errUnsafe) || len(data) != 0 {
				t.Fatalf("Read must reject mutated root: bytes=%d err=%v", len(data), err)
			}
			if child, err := root.OpenDirectory([]string{"child"}); !errors.Is(err, errUnsafe) || child != nil {
				if child != nil {
					child.Close()
				}
				t.Fatalf("OpenDirectory must reject mutated root: %v", err)
			}
			// Simulate mutation AFTER the initial inspect: use the existing raw relative
			// open. Query identity only, never consume the external file's contents.
			leaf, err := openRelative(guard, "sentinel", false)
			if leaf != 0 {
				handles = append(handles, leaf)
			}
			if err != nil {
				if !errors.Is(err, windows.STATUS_OBJECT_NAME_NOT_FOUND) &&
					!errors.Is(err, windows.STATUS_OBJECT_PATH_NOT_FOUND) &&
					!errors.Is(err, windows.STATUS_REPARSE_POINT_ENCOUNTERED) &&
					!errors.Is(err, windows.STATUS_DIRECTORY_IS_A_REPARSE_POINT) {
					t.Fatalf("unqualified raw-open error (not containment proof): %v", err)
				}
				t.Logf("held-root relative open refused after mutation: %v", err)
				return
			}
			var got idInfo
			if err := query(leaf, windows.FileIdInfo, &got); err != nil {
				t.Fatal(err)
			}
			if got == outsideID {
				t.Fatal("UNSAFE: relative open through mutated held directory reached outside sentinel")
			}
			t.Fatal("unexpected relative-open success in initially empty directory; cannot qualify containment")
		})
	}
}

func reparseMutationOpen(parent windows.Handle, right, share uint32) (windows.Handle, error) {
	n, err := windows.NewNTUnicodeString("victim")
	if err != nil {
		return 0, err
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: parent, ObjectName: n}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var h windows.Handle
	var io windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&h, right|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, &oa, &io, nil, 0, share, windows.FILE_OPEN, windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_NO_RECALL|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	return h, err
}
func reparseMutationBuffer(t *testing.T, target string) []byte {
	t.Helper()
	sub, err := windows.UTF16FromString(`\??\` + target)
	if err != nil {
		t.Fatal(err)
	}
	print, err := windows.UTF16FromString(target)
	if err != nil {
		t.Fatal(err)
	}
	length := 8 + 2*(len(sub)+len(print))
	if length+8 > 16384 {
		t.Fatal("mount point payload too large")
	}
	b := make([]byte, 8+length)
	binary.LittleEndian.PutUint32(b, windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(b[4:], uint16(length))
	binary.LittleEndian.PutUint16(b[10:], uint16(2*(len(sub)-1)))
	binary.LittleEndian.PutUint16(b[12:], uint16(2*len(sub)))
	binary.LittleEndian.PutUint16(b[14:], uint16(2*(len(print)-1)))
	for i, v := range append(sub, print...) {
		binary.LittleEndian.PutUint16(b[16+2*i:], v)
	}
	return b
}
