//go:build windows

package windowsprobe

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"syscall"
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
	files        map[windows.Handle]bool
	types        map[string]int
	invalidTypes map[windows.Handle]string
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
	fileType := dll.NewProc("GetFileType")
	for _, proc := range []*windows.LazyProc{capture, free, createMarker, freeMarker, walk, fileType} {
		if err := proc.Find(); err != nil {
			t.Fatal(err)
		}
	}
	// x/sys GetFileType maps a zero result plus ERROR_SUCCESS to EINVAL.
	// The public API permits FILE_TYPE_UNKNOWN with no error; preserve that
	// distinction using the raw call (Go's syscall trampoline clears LastError).
	getFileType := func(h windows.Handle) (uint32, error) {
		kind, _, err := fileType.Call(uintptr(h))
		if kind != 0 || err == syscall.Errno(0) {
			return uint32(kind), nil
		}
		return 0, err
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
	result := handleInventory{files: map[windows.Handle]bool{}, types: map[string]int{}, invalidTypes: map[windows.Handle]string{}}
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
		// Classify the current value first: some non-file objects cannot be
		// duplicated. Disk results are then pinned and rechecked. This is a
		// controlled persistent-disk-leak detector, not an atomic process audit.
		kind, queryErr := getFileType(entry.Handle)
		if queryErr == nil && kind == windows.FILE_TYPE_DISK {
			var duplicate windows.Handle
			if err := windows.DuplicateHandle(windows.CurrentProcess(), entry.Handle, windows.CurrentProcess(), &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
				t.Fatalf("duplicate captured disk handle %#x: %v", entry.Handle, err)
			}
			confirmed, confirmErr := getFileType(duplicate)
			closeErr := windows.CloseHandle(duplicate)
			if confirmErr != nil || confirmed != windows.FILE_TYPE_DISK || closeErr != nil {
				t.Fatalf("disk classification changed or close failed: kind=%d query=%v close=%v", confirmed, confirmErr, closeErr)
			}
		}
		typ := ""
		if queryErr != nil {
			if !errors.Is(queryErr, windows.ERROR_INVALID_HANDLE) {
				t.Fatalf("GetFileType captured handle %#x: %v", entry.Handle, queryErr)
			}
			// Could be a non-file object or a value closed since the snapshot.
			// Do not call it a Thread/Event or infer its former object type.
			typ = "InvalidForGetFileType"
			observed, typeErr := observeHandleObjectType(entry.Handle)
			if typeErr != nil {
				observed = "Unclassified: " + typeErr.Error()
			}
			result.invalidTypes[entry.Handle] = observed
		} else {
			switch kind {
			case windows.FILE_TYPE_DISK:
				typ = "Disk"
				result.files[entry.Handle] = true
			case windows.FILE_TYPE_PIPE:
				typ = "Pipe"
			case windows.FILE_TYPE_CHAR:
				typ = "Character"
			case windows.FILE_TYPE_UNKNOWN:
				typ = "UnknownFileType"
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
	beforeTypes, afterTypes := map[string]int{}, map[string]int{}
	for _, typ := range before.invalidTypes {
		beforeTypes[typ]++
	}
	for h, typ := range after.invalidTypes {
		afterTypes[typ]++
		if old, exists := before.invalidTypes[h]; !exists || old != typ {
			t.Logf("InvalidForGetFileType observed handle=%#x before=%q after=%q (live-value observation, not snapshot identity)", h, old, typ)
		}
	}
	typeKeys := map[string]bool{}
	for typ := range beforeTypes {
		typeKeys[typ] = true
	}
	for typ := range afterTypes {
		typeKeys[typ] = true
	}
	labels := make([]string, 0, len(typeKeys))
	for typ := range typeKeys {
		labels = append(labels, typ)
	}
	sort.Strings(labels)
	for _, typ := range labels {
		t.Logf("InvalidForGetFileType observed object type=%s before=%d after=%d delta=%+d", typ, beforeTypes[typ], afterTypes[typ], afterTypes[typ]-beforeTypes[typ])
	}

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

// ObjectTypeInformation (2) exposes a type label, never an object pathname.
// The original value can close/recycle after PSS; this is diagnostic only.
var queryDiagnosticObjectType = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQueryObject")

func observeHandleObjectType(h windows.Handle) (string, error) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		return "", errors.New("unsupported object-type ABI")
	}
	if err := queryDiagnosticObjectType.Find(); err != nil {
		return "", err
	}
	var storage [512]uint64 // fixed 4096 bytes, 8-byte aligned
	buf := unsafe.Slice((*byte)(unsafe.Pointer(&storage[0])), 4096)
	var returned uint32
	status, _, _ := queryDiagnosticObjectType.Call(uintptr(h), 2, uintptr(unsafe.Pointer(&storage[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&returned)))
	if uint32(status) != 0 {
		return "", fmt.Errorf("NtQueryObject status=%#x returned=%d", uint32(status), returned)
	}
	// PUBLIC_OBJECT_TYPE_INFORMATION: 16-byte UNICODE_STRING + ULONG[22].
	if returned < 104 || returned > uint32(len(buf)) {
		return "", fmt.Errorf("invalid object-type return length=%d", returned)
	}
	length := uint64(binary.LittleEndian.Uint16(buf[0:2]))
	maximum := uint64(binary.LittleEndian.Uint16(buf[2:4]))
	pointer := binary.LittleEndian.Uint64(buf[8:16])
	base := uint64(uintptr(unsafe.Pointer(&storage[0])))
	if length == 0 || length%2 != 0 || maximum%2 != 0 || maximum < length || pointer < base {
		return "", errors.New("invalid object-type UNICODE_STRING")
	}
	offset := pointer - base
	if offset < 104 || offset%2 != 0 || offset > uint64(returned) || maximum > uint64(returned)-offset {
		return "", errors.New("object-type string exceeds returned buffer")
	}
	// Decode through the original buffer, never by converting the returned
	// integer address into a Go pointer. Type labels must be printable ASCII.
	name := make([]byte, length/2)
	for i := range name {
		at := int(offset) + 2*i
		u := binary.LittleEndian.Uint16(buf[at : at+2])
		if u < 0x20 || u > 0x7e {
			return "", errors.New("invalid object-type label code unit")
		}
		name[i] = byte(u)
	}
	return string(name), nil
}

func TestNativeObjectTypeDiagnosticPositiveControl(t *testing.T) {
	event, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := windows.CloseHandle(event); err != nil {
			t.Error(err)
		}
	}()
	if typ, err := observeHandleObjectType(event); err != nil || typ != "Event" {
		t.Fatalf("Event type positive control: %q %v", typ, err)
	}
	_, root := fixture(t)
	if typ, err := observeHandleObjectType(root.h); err != nil || typ != "File" {
		t.Fatalf("File type positive control: %q %v", typ, err)
	}
	if typ, err := observeHandleObjectType(0); err == nil || typ != "" {
		t.Fatalf("invalid handle fabricated type: %q %v", typ, err)
	}
}
