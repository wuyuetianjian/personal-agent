//go:build windows

package windowsprobe

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Limit is the maximum accepted file size. Reads use at most Limit+1 bytes.
const Limit = 256 * 1024

var ErrTooLarge = errors.New("windowsprobe: file exceeds 256 KiB")

// Root is an experimental authority over an already-open directory object, not
// an authorization of its pathname. It serializes reads and Close.
type Root struct {
	mu sync.Mutex
	h  windows.Handle
	id idInfo
}
type idInfo struct {
	Volume uint64
	ID     [16]byte
}
type basicInfo struct {
	Creation, Access, Write, Change int64
	Attributes                      uint32
	_                               uint32
}
type standardInfo struct {
	Allocation, Size         int64
	Links                    uint32
	DeletePending, Directory byte
	_                        [2]byte
}
type tagInfo struct{ Attributes, Tag uint32 }
type snapshot struct {
	ID       idInfo
	Basic    basicInfo
	Standard standardInfo
	Tag      tagInfo
}

// NewRoot duplicates a caller-owned trusted local NTFS directory handle. The
// caller must obtain it without following untrusted namespace substitutions,
// with FILE_LIST_DIRECTORY, FILE_READ_ATTRIBUTES and SYNCHRONIZE access,
// no FILE_SHARE_DELETE, no delete access, and no backup privilege bypass.
// This API cannot inspect/strengthen the caller's original sharing contract.
// Closing the original handle after success is safe. No path-binding API exists.
func NewRoot(trusted windows.Handle) (*Root, error) {
	var h windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), trusted, windows.CurrentProcess(), &h, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return nil, err
	}
	if err := rootAccess(h); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	s, err := inspect(h)
	if err != nil || s.Standard.Directory == 0 {
		windows.CloseHandle(h)
		if err == nil {
			err = errUnsafe
		}
		return nil, err
	}
	return &Root{h: h, id: s.ID}, nil
}

// rootAccess verifies a participating I/O access mask, not the original share
// mode. The latter remains an explicit trusted-caller precondition.
func rootAccess(h windows.Handle) error {
	var access uint32
	var io windows.IO_STATUS_BLOCK
	const fileAccessInformation = 8
	err := windows.NtQueryInformationFile(h, &io, (*byte)(unsafe.Pointer(&access)), uint32(unsafe.Sizeof(access)), fileAccessInformation)
	if err != nil {
		return err
	}
	required := uint32(windows.FILE_LIST_DIRECTORY | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE)
	if io.Status != 0 || io.Information != unsafe.Sizeof(access) || access&required != required || access&windows.DELETE != 0 {
		return errUnsafe
	}
	return nil
}

func (r *Root) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.h == 0 {
		return nil
	}
	h := r.h
	r.h = 0
	return windows.CloseHandle(h)
}

func query[T any](h windows.Handle, class uint32, value *T) error {
	return windows.GetFileInformationByHandleEx(h, class, (*byte)(unsafe.Pointer(value)), uint32(unsafe.Sizeof(*value)))
}

var queryVolume = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQueryVolumeInformationFile")

func localDisk(h windows.Handle) error {
	typ, err := windows.GetFileType(h)
	if err != nil {
		return err
	}
	if typ != windows.FILE_TYPE_DISK {
		return errUnsafe
	}
	// FILE_FS_DEVICE_INFORMATION, FileFsDeviceInformation=4. FILE_REMOTE_DEVICE
	// rejects network redirectors even if their filesystem is reported as NTFS.
	var device struct{ Type, Characteristics uint32 }
	var io windows.IO_STATUS_BLOCK
	if err := queryVolume.Find(); err != nil {
		return err
	}
	status, _, _ := queryVolume.Call(uintptr(h), uintptr(unsafe.Pointer(&io)), uintptr(unsafe.Pointer(&device)), unsafe.Sizeof(device), 4)
	// The documented API returns final completion, STATUS_SUCCESS or error.
	// Require exact success and a complete FILE_FS_DEVICE_INFORMATION.
	if status != 0 {
		return windows.NTStatus(status)
	}
	if io.Status != 0 || io.Information != unsafe.Sizeof(device) || device.Type != 7 || device.Characteristics&0x10 != 0 {
		return errUnsafe
	}
	return nil
}
func inspect(h windows.Handle) (s snapshot, err error) {
	if err = localDisk(h); err != nil {
		return
	}
	var fs [32]uint16
	if err = windows.GetVolumeInformationByHandle(h, nil, 0, nil, nil, nil, &fs[0], uint32(len(fs))); err != nil {
		return
	}
	if windows.UTF16ToString(fs[:]) != "NTFS" {
		return s, errUnsafe
	}
	if err = query(h, windows.FileIdInfo, &s.ID); err != nil {
		return
	}
	if err = query(h, windows.FileBasicInfo, &s.Basic); err != nil {
		return
	}
	if err = query(h, windows.FileStandardInfo, &s.Standard); err != nil {
		return
	}
	if err = query(h, windows.FileAttributeTagInfo, &s.Tag); err != nil {
		return
	}
	const forbidden = windows.FILE_ATTRIBUTE_REPARSE_POINT | windows.FILE_ATTRIBUTE_OFFLINE | windows.FILE_ATTRIBUTE_DEVICE | windows.FILE_ATTRIBUTE_RECALL_ON_OPEN | windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS
	if s.Basic.Attributes&forbidden != 0 || s.Tag.Attributes&forbidden != 0 || s.Tag.Tag != 0 || s.Standard.DeletePending != 0 || s.Standard.Size < 0 || (s.Standard.Directory == 0 && s.Standard.Links != 1) {
		return s, errUnsafe
	}
	if s.Standard.Directory != 0 {
		var flags uint32
		if err = query(h, windows.FileCaseSensitiveInfo, &flags); err != nil {
			return
		}
		if flags != 0 {
			return s, errUnsafe
		}
	}
	return
}
func exactName(h windows.Handle, name string) error {
	// Normalized long name, not an opened/short name; never used for authority.
	var buf [32768]uint16
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
	if err != nil {
		return err
	}
	if n == 0 || n >= uint32(len(buf)) {
		return errUnsafe
	}
	p := windows.UTF16ToString(buf[:n])
	i := strings.LastIndexByte(p, '\\')
	if i < 0 || p[i+1:] != name {
		return errUnsafe
	}
	return nil
}
func openRelative(parent windows.Handle, name string, data bool) (windows.Handle, error) {
	n, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: parent, ObjectName: n}
	oa.Length = uint32(unsafe.Sizeof(oa))
	// OBJ_CASE_INSENSITIVE and OBJ_INHERIT deliberately absent. Reparse objects
	// are opened as metadata then rejected, without FILE_DIRECTORY_FILE.
	access := uint32(windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE)
	if data {
		access |= windows.FILE_READ_DATA
	}
	var h windows.Handle
	var io windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&h, access, &oa, &io, nil, 0, windows.FILE_SHARE_READ, windows.FILE_OPEN, windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_NO_RECALL|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	return h, err
}

// acquireIOGuard is the metadata-to-I/O boundary. Metadata-only handles do
// NOT participate in ordinary read/write/delete sharing checks. Requesting
// FILE_READ_DATA (the same bit as FILE_LIST_DIRECTORY) creates a participating
// no-delete guard. Never traverse/read it until full identity is checked.
func acquireIOGuard(parent windows.Handle, name string, expected snapshot) (windows.Handle, snapshot, error) {
	h, err := openRelative(parent, name, true)
	if err != nil {
		return 0, snapshot{}, err
	}
	actual, err := inspect(h)
	if err == nil && !unchanged(expected, actual) {
		err = errUnsafe
	}
	if err == nil {
		err = exactName(h, name)
	}
	if err != nil {
		windows.CloseHandle(h)
		return 0, snapshot{}, err
	}
	return h, actual, nil
}

func unchanged(a, b snapshot) bool {
	// Last-access time may change as a consequence of reading. Creation time is
	// only a contemporaneous consistency field, never a durable anti-reuse token.
	a.Basic.Access = 0
	b.Basic.Access = 0
	return a == b
}

// Read traverses exact individual long-name components and returns bounded file
// data only after identity and metadata revalidation. It does not provide a
// snapshot or hard cancellation of synchronous filesystem/filter calls.
func (r *Root) Read(ctx context.Context, components []string) ([]byte, error) {
	if err := validateComponents(components); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.h == 0 {
		return nil, errors.New("windowsprobe: closed root")
	}
	root, err := inspect(r.h)
	if err != nil {
		return nil, err
	}
	if root.ID != r.id {
		return nil, errUnsafe
	}
	handles := []windows.Handle{}
	defer func() {
		for i := len(handles) - 1; i >= 0; i-- {
			windows.CloseHandle(handles[i])
		}
	}()
	parent := r.h
	type guard struct {
		h    windows.Handle
		s    snapshot
		name string
	}
	guards := []guard{}
	for i, name := range components {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h, err := openRelative(parent, name, false)
		if err != nil {
			return nil, err
		}
		handles = append(handles, h)
		s, err := inspect(h)
		if err != nil {
			return nil, err
		}
		if s.ID.Volume != r.id.Volume {
			return nil, errUnsafe
		}
		if err = exactName(h, name); err != nil {
			return nil, err
		}
		guards = append(guards, guard{h, s, name})
		leaf := i == len(components)-1
		if !leaf {
			if s.Standard.Directory == 0 {
				return nil, errUnsafe
			}
			ioGuard, qualified, err := acquireIOGuard(parent, name, s)
			if err != nil {
				return nil, err
			}
			handles = append(handles, ioGuard)
			guards = append(guards, guard{ioGuard, qualified, name})
			parent = ioGuard
			continue
		}
		if s.Standard.Directory != 0 {
			return nil, errUnsafe
		}
		if s.Standard.Size > Limit {
			return nil, ErrTooLarge
		}
		// ReOpenFile's documented contract requires a CreateFile-origin handle;
		// these are NtCreateFile-origin handles. Instead, reopen one exact basename
		// beneath the pinned parent and compare full identity BEFORE the first read.
		data, before, err := acquireIOGuard(parent, name, s)
		if err != nil {
			return nil, err
		}
		handles = append(handles, data)
		result := make([]byte, 0, int(s.Standard.Size))
		chunk := make([]byte, 32768)
		for len(result) <= Limit {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			take := len(chunk)
			if remain := Limit + 1 - len(result); take > remain {
				take = remain
			}
			var n uint32
			err = windows.ReadFile(data, chunk[:take], &n, nil)
			if err != nil && !errors.Is(err, windows.ERROR_HANDLE_EOF) {
				return nil, err
			}
			result = append(result, chunk[:n]...)
			if len(result) > Limit {
				return nil, ErrTooLarge
			}
			if n == 0 || errors.Is(err, windows.ERROR_HANDLE_EOF) {
				break
			}
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		after, err := inspect(data)
		if err != nil {
			return nil, err
		}
		if !unchanged(before, after) || int64(len(result)) != s.Standard.Size {
			return nil, errUnsafe
		}
		for _, g := range guards {
			now, e := inspect(g.h)
			if e != nil {
				return nil, e
			}
			if !unchanged(g.s, now) {
				return nil, errUnsafe
			}
			if e = exactName(g.h, g.name); e != nil {
				return nil, e
			}
		}
		now, err := inspect(r.h)
		if err != nil {
			return nil, err
		}
		if now.ID != r.id || now.Standard.Directory == 0 {
			return nil, errUnsafe
		}
		return result, nil
	}
	return nil, fmt.Errorf("windowsprobe: no leaf")
}
