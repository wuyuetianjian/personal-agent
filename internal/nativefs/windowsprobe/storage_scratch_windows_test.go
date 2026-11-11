//go:build windows

package windowsprobe

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

const scratchAccess = windows.FILE_READ_DATA | windows.FILE_WRITE_DATA | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE | windows.DELETE

// All helpers are test-only. This is kernel qualification, not a writable API.
func scratchName(t *testing.T) string {
	t.Helper()
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		t.Fatal(err)
	}
	return "scratch-" + hex.EncodeToString(entropy[:])
}
func scratchNTName(h windows.Handle) (string, error) {
	var b [32768]uint16
	n, err := windows.GetFinalPathNameByHandle(h, &b[0], uint32(len(b)), 2 /* VOLUME_NAME_NT, normalized */)
	if err != nil {
		return "", err
	}
	if n == 0 || n >= uint32(len(b)) {
		return "", errors.New("invalid native filename length")
	}
	return windows.UTF16ToString(b[:n]), nil
}
func scratchCreate(parent windows.Handle, name string) (windows.Handle, windows.IO_STATUS_BLOCK, error) {
	var io windows.IO_STATUS_BLOCK
	if err := validateNames([]string{name}, false); err != nil {
		return 0, io, err
	}
	n, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, io, err
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: parent, ObjectName: n}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var h windows.Handle
	err = windows.NtCreateFile(&h, scratchAccess, &oa, &io, nil, windows.FILE_ATTRIBUTE_NORMAL, 0, windows.FILE_CREATE,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_NO_RECALL|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_DELETE_ON_CLOSE, 0, 0)
	return h, io, err
}
func scratchLanding(t *testing.T, h windows.Handle, parentNT, name string) {
	t.Helper()
	actual, err := scratchNTName(h)
	expected := strings.TrimRight(parentNT, `\`) + `\` + name
	if err != nil || actual != expected {
		// Caller already deferred HANDLE-only close. No content I/O or path cleanup.
		t.Fatalf("scratch escaped/unknown landing BEFORE write/close: expected=%q actual=%q error=%v", expected, actual, err)
	}
}

// Consume ownership before closing: an uncertain close must never be retried.
func scratchClose(t *testing.T, owned *windows.Handle) {
	t.Helper()
	h := *owned
	*owned = 0
	if h != 0 {
		if err := windows.CloseHandle(h); err != nil {
			t.Errorf("close scratch fixture handle: %v", err)
		}
	}
}
func scratchID(t *testing.T, h windows.Handle) idInfo {
	t.Helper()
	var id idInfo
	if err := query(h, windows.FileIdInfo, &id); err != nil {
		t.Fatal(err)
	}
	if id.ID == [16]byte{} {
		t.Fatal("zero file identity")
	}
	return id
}

// Snapshot immediately, then close before collision creation to avoid introducing
// sharing failures into the FILE_CREATE oracle. Never use lazy os.FileInfo IDs.
func scratchPathID(t *testing.T, path string) idInfo {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer scratchClose(t, &h)
	return scratchID(t, h)
}
func scratchQualify(t *testing.T, h, parent windows.Handle, io windows.IO_STATUS_BLOCK) idInfo {
	t.Helper()
	if io.Status != 0 || io.Information != 2 /* FILE_CREATED */ {
		t.Fatalf("create IOSB status=%x information=%d", io.Status, io.Information)
	}
	if err := localDisk(h); err != nil {
		t.Fatal(err)
	}
	var fs [32]uint16
	if err := windows.GetVolumeInformationByHandle(h, nil, 0, nil, nil, nil, &fs[0], uint32(len(fs))); err != nil || windows.UTF16ToString(fs[:]) != "NTFS" {
		t.Fatalf("scratch filesystem: %v", err)
	}
	var standard standardInfo
	var tag tagInfo
	var basic basicInfo
	if err := errors.Join(query(h, windows.FileStandardInfo, &standard), query(h, windows.FileAttributeTagInfo, &tag), query(h, windows.FileBasicInfo, &basic)); err != nil {
		t.Fatal(err)
	}
	const forbidden = windows.FILE_ATTRIBUTE_DIRECTORY | windows.FILE_ATTRIBUTE_REPARSE_POINT | windows.FILE_ATTRIBUTE_OFFLINE | windows.FILE_ATTRIBUTE_DEVICE | windows.FILE_ATTRIBUTE_RECALL_ON_OPEN | windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS
	if standard.Directory != 0 || standard.Links != 1 || standard.Size != 0 || standard.DeletePending > 1 || tag.Tag != 0 || (tag.Attributes|basic.Attributes)&forbidden != 0 {
		t.Fatalf("invalid scratch metadata: standard=%+v tag=%+v", standard, tag)
	}
	t.Logf("DELETE_ON_CLOSE observed DeletePending=%d", standard.DeletePending)
	var access uint32
	var accessIO windows.IO_STATUS_BLOCK
	if err := windows.NtQueryInformationFile(h, &accessIO, (*byte)(unsafe.Pointer(&access)), 4, 8); err != nil {
		t.Fatal(err)
	}
	if accessIO.Status != 0 || accessIO.Information != 4 || access != scratchAccess {
		t.Fatalf("scratch granted access=%x status=%x size=%d", access, accessIO.Status, accessIO.Information)
	}
	p := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation")
	if err := p.Find(); err != nil {
		t.Fatal(err)
	}
	var flags uint32
	ok, _, err := p.Call(uintptr(h), uintptr(unsafe.Pointer(&flags)))
	if ok == 0 || flags&windows.HANDLE_FLAG_INHERIT != 0 {
		t.Fatalf("scratch inherit/flags=%x error=%v", flags, err)
	}
	id := scratchID(t, h)
	if parentID := scratchID(t, parent); id.Volume != parentID.Volume {
		t.Fatalf("scratch volume differs from pinned parent: child=%+v parent=%+v", id, parentID)
	}
	return id
}
func scratchFixture(t *testing.T) (string, *Root) {
	t.Helper()
	dir, err := os.MkdirTemp("", "agent-scratch-")
	if err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		if e := os.Remove(dir); e != nil {
			t.Error(e)
		}
		t.Fatal(err)
	}
	root, err := NewRoot(h)
	closeErr := windows.CloseHandle(h)
	if err != nil || closeErr != nil {
		if root != nil {
			if e := root.Close(); e != nil {
				t.Error(e)
			}
		}
		if e := os.Remove(dir); e != nil {
			t.Error(e)
		}
		t.Fatalf("scratch root: %v / %v", err, closeErr)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
		if err := os.Remove(dir); err != nil {
			t.Errorf("nonrecursive root cleanup: %v", err)
		}
	})
	return dir, root
}
func scratchSentinel(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "unchanged-sentinel" {
		t.Fatalf("sentinel changed: %q %v", data, err)
	}
}

func TestNativeStorageScratchLifecycle(t *testing.T) {
	dir, root := scratchFixture(t)
	parentNT, err := scratchNTName(root.h)
	if err != nil {
		t.Fatal(err)
	}
	captureHandleInventory(t)
	baseline := captureHandleInventory(t)
	for i := 0; i < 8; i++ {
		func() {
			name := scratchName(t)
			h, io, err := scratchCreate(root.h, name)
			defer scratchClose(t, &h)
			if err != nil || h == 0 {
				t.Fatalf("scratch create: %v", err)
			}
			scratchLanding(t, h, parentNT, name)
			identity := scratchQualify(t, h, root.h, io)
			content := []byte("bounded scratch proof\n")
			if len(content) > 256 {
				t.Fatal("test payload too large")
			}
			var written uint32
			if err := windows.WriteFile(h, content, &written, nil); err != nil || written != uint32(len(content)) {
				t.Fatalf("scratch write=%d error=%v", written, err)
			}
			if after := scratchID(t, h); after != identity {
				t.Fatalf("scratch identity changed after write: before=%+v after=%+v", identity, after)
			}
			if err := windows.FlushFileBuffers(h); err != nil {
				t.Fatal(err)
			}
			if after := scratchID(t, h); after != identity {
				t.Fatalf("scratch identity changed after flush: before=%+v after=%+v", identity, after)
			}
			if offset, err := windows.Seek(h, 0, 0); err != nil || offset != 0 {
				t.Fatalf("seek=%d %v", offset, err)
			}
			read := make([]byte, len(content)+1)
			var n uint32
			if err := windows.ReadFile(h, read, &n, nil); err != nil || !bytes.Equal(read[:n], content) {
				t.Fatalf("scratch read=%d error=%v", n, err)
			}
			if after := scratchID(t, h); after != identity {
				t.Fatalf("scratch identity changed through write/flush/read: before=%+v after=%+v", identity, after)
			}
			scratchClose(t, &h)
			if t.Failed() {
				t.FailNow()
			}
			if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
				t.Fatalf("delete-on-close did not remove scratch: %v", err)
			}
		}()
	}
	after := captureHandleInventory(t)
	if !sameDiskHandles(baseline, after) {
		t.Fatalf("scratch disk handles leaked: before=%v after=%v", baseline.files, after.files)
	}
}

func TestNativeStorageScratchExistingCollisions(t *testing.T) {
	for _, kind := range []string{"regular", "directory", "junction"} {
		t.Run(kind, func(t *testing.T) {
			dir, root := scratchFixture(t)
			target := filepath.Join(dir, "victim")
			var attacker windows.Handle
			tagged := false
			outside, outRoot := scratchFixture(t)
			_ = outRoot
			sentinel := filepath.Join(outside, "sentinel")
			if err := os.WriteFile(sentinel, []byte("unchanged-sentinel"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				scratchSentinel(t, sentinel)
				if err := os.Remove(sentinel); err != nil {
					t.Error(err)
				}
			})
			if kind == "regular" {
				if err := os.WriteFile(target, []byte("unchanged-sentinel"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() {
				if tagged {
					var payload [8]byte
					binary.LittleEndian.PutUint32(payload[:], windows.IO_REPARSE_TAG_MOUNT_POINT)
					var n uint32
					if err := windows.DeviceIoControl(attacker, windows.FSCTL_DELETE_REPARSE_POINT, &payload[0], 8, nil, 0, &n, nil); err != nil {
						t.Errorf("remove fixture tag: %v", err)
					}
				}
				if attacker != 0 {
					scratchClose(t, &attacker)
				}
				if err := os.Remove(target); err != nil {
					t.Errorf("nonrecursive collision cleanup: %v", err)
				}
			})
			if kind == "junction" {
				var err error
				attacker, err = reparseMutationOpen(root.h, windows.FILE_WRITE_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
				if err != nil {
					t.Fatal(err)
				}
				payload := reparseMutationBuffer(t, outside)
				var n uint32
				if err := windows.DeviceIoControl(attacker, windows.FSCTL_SET_REPARSE_POINT, &payload[0], uint32(len(payload)), nil, 0, &n, nil); err != nil {
					t.Fatal(err)
				}
				tagged = true
				a, e1 := os.Stat(filepath.Join(target, "sentinel"))
				b, e2 := os.Stat(sentinel)
				if e1 != nil || e2 != nil || !os.SameFile(a, b) {
					t.Fatalf("collision junction target not established: %v / %v", e1, e2)
				}
			}
			before := scratchPathID(t, target)
			h, _, err := scratchCreate(root.h, "victim")
			if h != 0 {
				defer scratchClose(t, &h)
				actual, observeErr := scratchNTName(h)
				t.Fatalf("collision returned live handle BEFORE close: actual=%q observe=%v create=%v", actual, observeErr, err)
			}
			if !errors.Is(err, windows.STATUS_OBJECT_NAME_COLLISION) {
				t.Fatalf("unqualified collision status (must be OBJECT_NAME_COLLISION, not generic refusal): %v", err)
			}
			if after := scratchPathID(t, target); after != before {
				t.Fatalf("collision changed existing identity: before=%+v after=%+v", before, after)
			}
			if kind == "regular" {
				scratchSentinel(t, target)
			}
			scratchSentinel(t, sentinel)
		})
	}
}

func TestNativeStorageScratchMutatedPinnedParent(t *testing.T) {
	base, root := scratchFixture(t)
	outside, _ := scratchFixture(t)
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("unchanged-sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		scratchSentinel(t, sentinel)
		if err := os.Remove(sentinel); err != nil {
			t.Error(err)
		}
	})
	victim := filepath.Join(base, "victim")
	if err := os.Mkdir(victim, 0700); err != nil {
		t.Fatal(err)
	}
	guard, err := reparseMutationOpen(root.h, windows.FILE_LIST_DIRECTORY, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
	if err != nil {
		t.Fatal(err)
	}
	attacker, err := reparseMutationOpen(root.h, windows.FILE_WRITE_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
	if err != nil {
		scratchClose(t, &guard)
		t.Fatal(err)
	}
	tagged := false
	t.Cleanup(func() {
		clean := true
		if tagged {
			var b [8]byte
			binary.LittleEndian.PutUint32(b[:], windows.IO_REPARSE_TAG_MOUNT_POINT)
			var n uint32
			if err := windows.DeviceIoControl(attacker, windows.FSCTL_DELETE_REPARSE_POINT, &b[0], 8, nil, 0, &n, nil); err != nil {
				t.Error(err)
				clean = false
			}
		}
		scratchClose(t, &attacker)
		scratchClose(t, &guard)
		if clean {
			if err := os.Remove(victim); err != nil {
				t.Error(err)
			}
		}
	})
	if s, err := inspect(guard); err != nil || s.Standard.Directory == 0 {
		t.Fatalf("initial guard: %v", err)
	}
	parentNT, err := scratchNTName(guard)
	if err != nil {
		t.Fatal(err)
	}
	// Establish this exact held guard supports the operation before mutation.
	func() {
		name := scratchName(t)
		h, io, err := scratchCreate(guard, name)
		defer scratchClose(t, &h)
		if err != nil || h == 0 {
			t.Fatalf("pre-mutation same-guard positive control: %v", err)
		}
		scratchLanding(t, h, parentNT, name)
		scratchQualify(t, h, guard, io)
		scratchClose(t, &h)
		if t.Failed() {
			t.FailNow()
		}
		if _, err := os.Lstat(filepath.Join(victim, name)); !os.IsNotExist(err) {
			t.Fatalf("positive-control cleanup: %v", err)
		}
	}()
	payload := reparseMutationBuffer(t, outside)
	var n uint32
	if err := windows.DeviceIoControl(attacker, windows.FSCTL_SET_REPARSE_POINT, &payload[0], uint32(len(payload)), nil, 0, &n, nil); err != nil {
		t.Fatalf("mutation not established: %v", err)
	}
	tagged = true
	// Independent fixture control proves the new junction really reaches outside.
	a, err := os.Stat(filepath.Join(victim, "sentinel"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(sentinel)
	if err != nil || !os.SameFile(a, b) {
		t.Fatalf("junction control mismatch: %v", err)
	}
	name := scratchName(t)
	h, io, createErr := scratchCreate(guard, name)
	if h != 0 {
		defer scratchClose(t, &h)
		scratchLanding(t, h, parentNT, name)
		scratchQualify(t, h, guard, io)
	}
	if createErr == nil && h == 0 {
		t.Fatal("create succeeded without handle")
	}
	if createErr != nil && h != 0 {
		t.Fatalf("create error with handle: %v", createErr)
	}
	if createErr != nil && !errors.Is(createErr, windows.STATUS_REPARSE_POINT_NOT_RESOLVED) {
		t.Fatalf("unqualified mutated-parent refusal (expected REPARSE_POINT_NOT_RESOLVED): %v", createErr)
	}
	// Never write through this changed parent, even if the kernel kept it local.
	t.Logf("mutated parent native result: handle_returned=%v error=%v", h != 0, createErr)
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 1 || entries[0].Name() != "sentinel" {
		t.Fatalf("outside changed before scratch close: %v %v", entries, err)
	}
	scratchSentinel(t, sentinel)
}
