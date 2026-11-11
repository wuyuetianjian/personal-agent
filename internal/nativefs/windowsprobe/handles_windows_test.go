//go:build windows

package windowsprobe

import (
	"fmt"
	"sort"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// PSS_HANDLE_ENTRY, Windows 64-bit SDK layout. Only handle/type are consumed;
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
		status, _, _ = walk.Call(uintptr(snapshot), 1 /* PSS_WALK_HANDLES */, uintptr(marker), uintptr(unsafe.Pointer(&entry)), unsafe.Sizeof(entry))
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
		if entry.Flags&1 == 0 || entry.TypeName == nil || entry.TypeNameLength == 0 || entry.TypeNameLength%2 != 0 || entry.TypeNameLength > 1024 {
			t.Fatalf("PSS missing/invalid type information: handle=%#x flags=%#x typeLength=%d", entry.Handle, entry.Flags, entry.TypeNameLength)
		}
		units := unsafe.Slice(entry.TypeName, int(entry.TypeNameLength)/2)
		// Kernel object type names are ASCII. Do not silently repair malformed UTF-16.
		name := make([]byte, len(units))
		for j, u := range units {
			if u < 0x20 || u > 0x7e {
				t.Fatalf("invalid object type code unit %#x", u)
			}
			name[j] = byte(u)
		}
		typ := string(name)
		result.types[typ]++
		if typ == "File" {
			result.files[entry.Handle] = true
		}
	}
	t.Fatal("PSS handle enumeration exceeded hard bound")
	return result
}

func sameFileHandles(a, b handleInventory) bool {
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
	t.Logf("PSS File handle sets equal=%t before=%v after=%v", sameFileHandles(before, after), before.files, after.files)
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
				t.Fatalf("PSS did not detect exactly one known extra File handle %#x: before=%v during=%v", h, before.files, during.files)
			}
			for old := range before.files {
				if !during.files[old] {
					t.Fatalf("PSS lost baseline File handle %#x", old)
				}
			}
			if err := windows.CloseHandle(h); err != nil {
				t.Fatal(err)
			}
			open = false
			after := captureHandleInventory(t)
			if !sameFileHandles(before, after) {
				t.Fatalf("File handles did not restore after closing control: before=%v after=%v", before.files, after.files)
			}
		})
	}
}
