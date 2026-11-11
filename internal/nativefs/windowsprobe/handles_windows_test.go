//go:build windows

package windowsprobe

import (
	"errors"
	"fmt"
	"sort"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// PSS_HANDLE_ENTRY, Windows 64-bit SDK layout. Only handle values are consumed;
// capture does not request object names, process cloning, or thread contexts.
type pssHandleEntry struct {
	Handle                                               windows.Handle
	Flags, ObjectType                                    uint32
	CaptureTime                                          windows.Filetime
	Attributes, GrantedAccess, HandleCount, PointerCount uint32
	PagedPoolCharge, NonPagedPoolCharge                  uint32
	CreationTime                                         windows.Filetime
	TypeNameLength                                       uint16
	TypeName                                             *uint16
	ObjectNameLength                                     uint16
	ObjectName                                           *uint16
	TypeSpecific                                         [6]uint64
}

type handleInventory struct {
	files map[windows.Handle]bool
	types map[string]int
}

func captureHandleInventory(t *testing.T) handleInventory {
	t.Helper()
	var layout pssHandleEntry
	if unsafe.Sizeof(uintptr(0)) != 8 || unsafe.Sizeof(layout) != 136 || unsafe.Offsetof(layout.TypeNameLength) != 56 || unsafe.Offsetof(layout.TypeName) != 64 || unsafe.Offsetof(layout.ObjectName) != 80 || unsafe.Offsetof(layout.TypeSpecific) != 88 {
		t.Fatal("unsupported PSS_HANDLE_ENTRY ABI")
	}
	dll := windows.NewLazySystemDLL("kernel32.dll")
	capture := dll.NewProc("PssCaptureSnapshot")
	free := dll.NewProc("PssFreeSnapshot")
	createMarker := dll.NewProc("PssWalkMarkerCreate")
	freeMarker := dll.NewProc("PssWalkMarkerFree")
	walk := dll.NewProc("PssWalkSnapshot")
	for _, proc := range []*windows.LazyProc{capture, free, createMarker, freeMarker, walk} {
		if err := proc.Find(); err != nil {
			t.Fatal(err)
		}
	}
	var snapshot, marker windows.Handle
	status, _, _ := capture.Call(uintptr(windows.CurrentProcess()), 4 /* PSS_CAPTURE_HANDLES only */, 0, uintptr(unsafe.Pointer(&snapshot)))
	if status != 0 {
		t.Fatalf("PssCaptureSnapshot: status=%d", status)
	}
	defer func() {
		status, _, _ := free.Call(uintptr(windows.CurrentProcess()), uintptr(snapshot))
		if status != 0 {
			t.Errorf("PssFreeSnapshot: status=%d", status)
		}
	}()
	status, _, _ = createMarker.Call(0, uintptr(unsafe.Pointer(&marker)))
	if status != 0 {
		t.Fatalf("PssWalkMarkerCreate: status=%d", status)
	}
	defer func() {
		status, _, _ := freeMarker.Call(uintptr(marker))
		if status != 0 {
			t.Errorf("PssWalkMarkerFree: status=%d", status)
		}
	}()
	result := handleInventory{files: map[windows.Handle]bool{}, types: map[string]int{}}
	all := map[windows.Handle]bool{}
	for i := 0; i < 65536; i++ {
		var entry pssHandleEntry
		status, _, _ = walk.Call(uintptr(snapshot), 2 /* PSS_WALK_HANDLES */, uintptr(marker), uintptr(unsafe.Pointer(&entry)), unsafe.Sizeof(entry))
		if status == uintptr(windows.ERROR_NO_MORE_ITEMS) {
			return result
		}
		if status != 0 {
			t.Fatalf("PssWalkSnapshot: status=%d", status)
		}
		if entry.Handle == 0 || entry.Handle == windows.InvalidHandle || all[entry.Handle] {
			t.Fatalf("invalid/duplicate PSS handle: %#x", entry.Handle)
		}
		all[entry.Handle] = true
		// PSS supplies handle values, not complete type metadata for every object.
		// Qualify a temporary duplicate using the public GetFileType API. This
		// pins the object while classifying it, but is not an atomic type audit
		// against arbitrary concurrent close/reuse between snapshot and duplicate.
		var duplicate windows.Handle
		if err := windows.DuplicateHandle(windows.CurrentProcess(), entry.Handle, windows.CurrentProcess(), &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
			t.Fatalf("duplicate captured handle %#x: %v", entry.Handle, err)
		}
		kind, queryErr := windows.GetFileType(duplicate)
		if err := windows.CloseHandle(duplicate); err != nil {
			t.Fatalf("close classification handle: %v", err)
		}
		typ := ""
		if queryErr != nil {
			if !errors.Is(queryErr, windows.ERROR_INVALID_HANDLE) {
				t.Fatalf("GetFileType captured handle %#x: %v", entry.Handle, queryErr)
			}
			// A successfully duplicated non-file object can be invalid for this
			// file-specific API. Do not call it a Thread/Event or infer its name.
			typ = "NotApplicableToGetFileType"
		} else {
			switch kind {
			case windows.FILE_TYPE_DISK:
				typ = "Disk"
				result.files[entry.Handle] = true
			case windows.FILE_TYPE_PIPE:
				typ = "Pipe"
			case windows.FILE_TYPE_CHAR:
				typ = "Character"
			default:
				t.Fatalf("unknown captured file type %#x for handle %#x", kind, entry.Handle)
			}
		}
		result.types[typ]++
	}
	t.Fatal("PSS handle enumeration exceeded hard bound")
	return result
}

func sameDiskHandles(a, b handleInventory) bool {
	if len(a.files) != len(b.files) {
		return false
	}
	for h := range a.files {
		if !b.files[h] {
			return false
		}
	}
	return true
}

func logHandleDelta(t *testing.T, before, after handleInventory) {
	t.Helper()
	keys := map[string]bool{}
	for k := range before.types {
		keys[k] = true
	}
	for k := range after.types {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		t.Logf("PSS type=%s before=%d after=%d delta=%+d", k, before.types[k], after.types[k], after.types[k]-before.types[k])
	}
	t.Logf("PSS Disk handle sets equal=%t before=%v after=%v", sameDiskHandles(before, after), before.files, after.files)
}

func TestNativeHandleInventoryPositiveControl(t *testing.T) {
	dir, _ := fixture(t)
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, access := range []uint32{windows.FILE_READ_ATTRIBUTES, windows.FILE_READ_ATTRIBUTES | windows.FILE_LIST_DIRECTORY | windows.SYNCHRONIZE} {
		t.Run(fmt.Sprintf("access_%x", access), func(t *testing.T) {
			captureHandleInventory(t) // warm PSS before taking the strict baseline
			before := captureHandleInventory(t)
			h, err := windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
			if err != nil {
				t.Fatal(err)
			}
			open := true
			defer func() {
				if open {
					windows.CloseHandle(h)
				}
			}()
			during := captureHandleInventory(t)
			if before.files[h] || !during.files[h] || len(during.files) != len(before.files)+1 {
				t.Fatalf("PSS did not detect exactly one known extra Disk handle %#x: before=%v during=%v", h, before.files, during.files)
			}
			for old := range before.files {
				if !during.files[old] {
					t.Fatalf("PSS lost baseline Disk handle %#x", old)
				}
			}
			if err := windows.CloseHandle(h); err != nil {
				t.Fatal(err)
			}
			open = false
			after := captureHandleInventory(t)
			if !sameDiskHandles(before, after) {
				t.Fatalf("Disk handles did not restore after closing control: before=%v after=%v", before.files, after.files)
			}
		})
	}
}
