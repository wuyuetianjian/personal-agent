//go:build windows && nativeprobe_sqlite_identity

package windowsprobe

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errScratchBusy = errors.New("scratch store busy")
var errScratchClosed = errors.New("scratch object closed")
var errScratchLimit = errors.New("scratch limit exceeded before IO")
var errScratchUncertain = errors.New("scratch IO outcome uncertain")

type scratchLimits struct {
	Files                 int
	FileBytes, TotalBytes int64
	IOBytes               int
}
type scratchHooks struct {
	afterCreate func(windows.Handle) error
	write       func(windows.Handle, []byte) (uint32, error)
	sync        func(windows.Handle) error
	truncate    func(windows.Handle, int64) error
	close       func(windows.Handle) error
}

// Private experiment, not a production writable capability.
// Lock order: file.mu -> store.mu. Store never acquires file.mu.
// Kernel file IO is serialized under both; Create retains a creating reference
// while outside store.mu so Close can revoke publication without freeing root.
type scratchStore struct {
	mu              sync.Mutex
	root            *Root
	ids             []idInfo // ancestors then selected root
	parentNT        string
	limits          scratchLimits
	hooks           scratchHooks
	files, creating int
	charged         int64
	closing         bool
	closingRoot     bool
	terminal        error
}
type scratchFile struct {
	mu               sync.Mutex
	h                windows.Handle
	store            *scratchStore
	id               idInfo
	size, charge     int64
	poison, closeErr error
}

const scratchOwnedAccess = windows.FILE_READ_DATA | windows.FILE_WRITE_DATA | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE | windows.DELETE

func newScratchStore(source *Root, limits scratchLimits, hooks scratchHooks) (out *scratchStore, err error) {
	if source == nil || limits.Files < 1 || limits.Files > 4 || limits.FileBytes < 1 || limits.FileBytes > 16<<20 || limits.TotalBytes < 1 || limits.TotalBytes > 32<<20 || limits.IOBytes < 1 || limits.IOBytes > 1<<20 {
		return nil, errScratchLimit
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.h == 0 {
		return nil, errScratchClosed
	}
	s := &scratchStore{root: &Root{depth: source.depth, id: source.id}, limits: limits, hooks: hooks}
	var owned []windows.Handle
	defer func() {
		if err != nil {
			for i := len(owned) - 1; i >= 0; i-- {
				err = errors.Join(err, windows.CloseHandle(owned[i]))
			}
		}
	}()
	sources := append(append([]windows.Handle(nil), source.ancestors...), source.h)
	for _, h := range sources {
		if e := rootAccess(h); e != nil {
			return nil, e
		}
		before, e := inspect(h)
		if e != nil {
			return nil, e
		}
		if before.Standard.Directory == 0 || before.ID.Volume != source.id.Volume {
			return nil, errUnsafe
		}
		var dup windows.Handle
		if e = windows.DuplicateHandle(windows.CurrentProcess(), h, windows.CurrentProcess(), &dup, 0, false, windows.DUPLICATE_SAME_ACCESS); e != nil {
			return nil, e
		}
		owned = append(owned, dup)
		after, e := inspect(dup)
		if e != nil {
			return nil, e
		}
		if after.ID != before.ID || after.Standard.Directory == 0 {
			return nil, errUnsafe
		}
		s.ids = append(s.ids, after.ID)
	}
	if s.ids[len(s.ids)-1] != source.id {
		return nil, errUnsafe
	}
	s.root.h = owned[len(owned)-1]
	s.root.ancestors = append([]windows.Handle(nil), owned[:len(owned)-1]...)
	s.parentNT, err = scratchOwnedName(s.root.h)
	if err != nil {
		return nil, err
	}
	if err = s.checkParent(); err != nil {
		return nil, err
	}
	return s, nil
}
func scratchOwnedName(h windows.Handle) (string, error) {
	b := make([]uint16, 32768)
	n, e := windows.GetFinalPathNameByHandle(h, &b[0], uint32(len(b)), 2)
	if e != nil {
		return "", e
	}
	if n == 0 || n >= uint32(len(b)) {
		return "", errUnsafe
	}
	return windows.UTF16ToString(b[:n]), nil
}

// Caller holds store.mu or a creating reference; owned lineage cannot close.
func (s *scratchStore) checkParent() error {
	if s.root == nil || s.root.h == 0 {
		return errScratchClosed
	}
	hs := append(append([]windows.Handle(nil), s.root.ancestors...), s.root.h)
	if len(hs) != len(s.ids) {
		return errUnsafe
	}
	for i, h := range hs {
		now, e := inspect(h)
		if e != nil {
			return e
		}
		if now.ID != s.ids[i] || now.Standard.Directory == 0 {
			return errUnsafe
		}
	}
	return nil
}
func scratchOwnedInspect(h windows.Handle) (id idInfo, size int64, err error) {
	if err = localDisk(h); err != nil {
		return
	}
	var fs [32]uint16
	if err = windows.GetVolumeInformationByHandle(h, nil, 0, nil, nil, nil, &fs[0], uint32(len(fs))); err != nil {
		return
	}
	if windows.UTF16ToString(fs[:]) != "NTFS" {
		return id, 0, errUnsafe
	}
	var st standardInfo
	var tag tagInfo
	var basic basicInfo
	err = errors.Join(query(h, windows.FileIdInfo, &id), query(h, windows.FileStandardInfo, &st), query(h, windows.FileAttributeTagInfo, &tag), query(h, windows.FileBasicInfo, &basic))
	if err != nil {
		return
	}
	const forbidden = windows.FILE_ATTRIBUTE_DIRECTORY | windows.FILE_ATTRIBUTE_REPARSE_POINT | windows.FILE_ATTRIBUTE_OFFLINE | windows.FILE_ATTRIBUTE_DEVICE | windows.FILE_ATTRIBUTE_RECALL_ON_OPEN | windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS
	if id.ID == [16]byte{} || st.Directory != 0 || st.Links != 1 || st.Size < 0 || st.DeletePending > 1 || tag.Tag != 0 || (tag.Attributes|basic.Attributes)&forbidden != 0 {
		return id, 0, errUnsafe
	}
	return id, st.Size, nil
}
func (s *scratchStore) Create() (file *scratchFile, err error) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return nil, errScratchClosed
	}
	if s.files+s.creating >= s.limits.Files {
		s.mu.Unlock()
		return nil, errScratchLimit
	}
	s.creating++
	s.mu.Unlock()
	var h windows.Handle
	defer func() {
		// Publication and close/revocation are linearized under store.mu.
		s.mu.Lock()
		defer s.mu.Unlock()
		s.creating--
		if err == nil && s.closing {
			err = errScratchClosed
		}
		if err == nil {
			err = s.checkParent()
			if err == nil {
				id, size, e := scratchOwnedInspect(h)
				err = e
				if err == nil && (file == nil || id != file.id || size != 0) {
					err = errUnsafe
				}
			}
		}
		if err != nil {
			file = nil
			if h != 0 {
				owned := h
				h = 0
				closeErr := windows.CloseHandle(owned)
				s.terminal = errors.Join(s.terminal, closeErr)
				if closeErr != nil {
					s.closing = true
				}
				err = errors.Join(err, closeErr)
			}
		} else {
			s.files++
			h = 0
		}
	}()
	if err = s.checkParent(); err != nil {
		return nil, err
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return nil, err
	}
	name := "scratch-" + hex.EncodeToString(random[:])
	n, e := windows.NewNTUnicodeString(name)
	if e != nil {
		return nil, e
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: s.root.h, ObjectName: n}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var ios windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&h, scratchOwnedAccess, &oa, &ios, nil, windows.FILE_ATTRIBUTE_NORMAL, 0, windows.FILE_CREATE, windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_NO_RECALL|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_DELETE_ON_CLOSE, 0, 0)
	if err != nil {
		return nil, err
	}
	if h == 0 || ios.Status != 0 || ios.Information != 2 {
		return nil, errUnsafe
	}
	actual, e := scratchOwnedName(h)
	if e != nil {
		return nil, e
	}
	if actual != strings.TrimRight(s.parentNT, `\`)+`\`+name {
		return nil, errUnsafe
	}
	id, size, e := scratchOwnedInspect(h)
	if e != nil {
		return nil, e
	}
	if id.Volume != s.root.id.Volume || size != 0 {
		return nil, errUnsafe
	}
	var access uint32
	var accessIO windows.IO_STATUS_BLOCK
	if e = windows.NtQueryInformationFile(h, &accessIO, (*byte)(unsafe.Pointer(&access)), 4, 8); e != nil {
		return nil, e
	}
	if access != scratchOwnedAccess || accessIO.Status != 0 || accessIO.Information != 4 {
		return nil, errUnsafe
	}
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation")
	if e = proc.Find(); e != nil {
		return nil, e
	}
	var flags uint32
	ok, _, callErr := proc.Call(uintptr(h), uintptr(unsafe.Pointer(&flags)))
	if ok == 0 {
		return nil, callErr
	}
	if flags&windows.HANDLE_FLAG_INHERIT != 0 {
		return nil, errUnsafe
	}
	if err = s.checkParent(); err != nil {
		return nil, err
	}
	if s.hooks.afterCreate != nil {
		if err = s.hooks.afterCreate(h); err != nil {
			return nil, err
		}
	}
	return &scratchFile{h: h, store: s, id: id}, nil
}
func (s *scratchStore) Close() error {
	s.mu.Lock()
	s.closing = true
	if s.files != 0 || s.creating != 0 || s.closingRoot {
		err := errors.Join(errScratchBusy, s.terminal)
		s.mu.Unlock()
		return err
	}
	root := s.root
	if root == nil {
		err := s.terminal
		s.mu.Unlock()
		return err
	}
	s.root = nil
	s.closingRoot = true
	s.mu.Unlock()
	// Never wait for a root mutex while holding the store account lock.
	err := root.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closingRoot = false
	s.terminal = errors.Join(s.terminal, err)
	return s.terminal
}
func (f *scratchFile) ready() error {
	if f.h == 0 {
		return errScratchClosed
	}
	if f.poison != nil {
		return f.poison
	}
	if e := f.store.checkParent(); e != nil {
		return e
	}
	id, size, e := scratchOwnedInspect(f.h)
	if e != nil {
		return e
	}
	if id != f.id || size != f.size {
		return errUnsafe
	}
	return nil
}
func (f *scratchFile) rangeOK(n int, off int64) bool {
	return n >= 0 && n <= f.store.limits.IOBytes && off >= 0 && off <= f.store.limits.FileBytes && int64(n) <= f.store.limits.FileBytes-off
}
func (f *scratchFile) seek(off int64) error {
	got, e := windows.Seek(f.h, off, 0)
	if e != nil {
		return e
	}
	if got != off {
		return errUnsafe
	}
	return nil
}
func (f *scratchFile) ReadAt(dst []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := f.ready(); e != nil {
		return 0, e
	}
	if !f.rangeOK(len(dst), off) {
		return 0, errScratchLimit
	}
	if len(dst) == 0 {
		return 0, nil
	}
	if e := f.seek(off); e != nil {
		return 0, e
	}
	var n uint32
	e := windows.ReadFile(f.h, dst, &n, nil)
	if int(n) > len(dst) {
		return 0, errUnsafe
	}
	if e != nil && !errors.Is(e, windows.ERROR_HANDLE_EOF) {
		return int(n), e
	}
	if check := f.ready(); check != nil {
		clear(dst)
		return 0, check
	}
	if n < uint32(len(dst)) {
		clear(dst[n:])
		if off+int64(n) < f.size {
			return int(n), io.ErrUnexpectedEOF
		}
		return int(n), io.EOF
	}
	if e := f.ready(); e != nil {
		return int(n), e
	}
	return int(n), nil
}
func (f *scratchFile) reserve(size int64) error {
	if size < 0 || size > f.store.limits.FileBytes {
		return errScratchLimit
	}
	delta := size - f.charge
	if delta > 0 && delta > f.store.limits.TotalBytes-f.store.charged {
		return errScratchLimit
	}
	// Keep conservative accounting after an uncertain syscall.
	if delta > 0 {
		f.store.charged += delta
		f.charge = size
	}
	return nil
}
func (f *scratchFile) poisonWith(e error) error {
	f.poison = errors.Join(errScratchUncertain, e)
	return f.poison
}
func (f *scratchFile) WriteAt(src []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := f.ready(); e != nil {
		return 0, e
	}
	if !f.rangeOK(len(src), off) {
		return 0, errScratchLimit
	}
	if len(src) == 0 {
		return 0, nil
	}
	end := off + int64(len(src))
	if end < f.size {
		end = f.size
	}
	if e := f.reserve(end); e != nil {
		return 0, e
	}
	if e := f.seek(off); e != nil {
		return 0, f.poisonWith(e)
	}
	var n uint32
	var e error
	if s.hooks.write != nil {
		n, e = s.hooks.write(f.h, src)
	} else {
		e = windows.WriteFile(f.h, src, &n, nil)
	}
	if e != nil || n != uint32(len(src)) {
		return int(n), f.poisonWith(errors.Join(e, io.ErrShortWrite))
	}
	f.size = end
	if e = f.ready(); e != nil {
		return int(n), f.poisonWith(e)
	}
	return int(n), nil
}
func (f *scratchFile) Truncate(size int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := f.ready(); e != nil {
		return e
	}
	if e := f.reserve(size); e != nil {
		return e
	}
	var e error
	if s.hooks.truncate != nil {
		e = s.hooks.truncate(f.h, size)
	} else {
		e = f.seek(size)
		if e == nil {
			e = windows.SetEndOfFile(f.h)
		}
	}
	if e != nil {
		return f.poisonWith(e)
	}
	f.size = size
	if e = f.ready(); e != nil {
		return f.poisonWith(e)
	}
	s.charged -= f.charge - size
	f.charge = size
	return nil
}
func (f *scratchFile) Size() (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	if e := f.ready(); e != nil {
		return 0, e
	}
	return f.size, nil
}
func (f *scratchFile) Sync() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	if e := f.ready(); e != nil {
		return e
	}
	var e error
	if f.store.hooks.sync != nil {
		e = f.store.hooks.sync(f.h)
	} else {
		e = windows.FlushFileBuffers(f.h)
	}
	if e != nil {
		return e
	}
	return f.ready()
}
func (f *scratchFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.h == 0 {
		return f.closeErr
	}
	h := f.h
	f.h = 0
	if s.hooks.close != nil {
		f.closeErr = s.hooks.close(h)
	} else {
		f.closeErr = windows.CloseHandle(h)
	}
	s.files--
	if f.closeErr == nil {
		s.charged -= f.charge
		f.charge = 0
	} else {
		s.closing = true
	}
	// Unknown-close quota remains reserved; terminal state bars all new creates.
	s.terminal = errors.Join(s.terminal, f.closeErr)
	return f.closeErr
}
