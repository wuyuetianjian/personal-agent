//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_lease && nativeprobe_sqlite_permit && nativeprobe_sqlite_existing_wal && nativeprobe_sqlite_shm_resources

package windowsprobe

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	sqlite3 "modernc.org/sqlite/lib"
	"sync"
	"unsafe"
)

var errSHMResourcesUnimplemented = errors.New("private retained SHM mapping not implemented")
var errSHMRetained = errors.New("SHM cleanup uncertain: backing and owner pin retained")
var errSHMFirstOpener = errors.New("existing SHM requires another DMS holder; initialization unsupported")

type shmResourceHooks struct {
	open          func(windows.Handle, string) (windows.Handle, error)
	mapping       func(windows.Handle) (windows.Handle, error)
	view          func(windows.Handle, uint64, uintptr) (uintptr, error)
	unmap         func(uintptr) error
	close         func(windows.Handle) error
	closeMetadata func(windows.Handle) error
	unlock        func(windows.Handle) error
	beforePublish func()
}
type shmResourceView struct {
	mapping          windows.Handle
	base             uintptr
	shift            uintptr
	length           uintptr
	attemptedBase    uintptr
	attemptedMapping windows.Handle
}
type shmResourceNode struct {
	h                                windows.Handle
	dms                              bool
	views                            map[int32]*shmResourceView
	retained                         bool
	unpublished                      []*shmResourceView
	attemptedMetadata, attemptedData windows.Handle
	attemptedDMSUnlock               bool
	exclusiveDMS                     bool
}
type shmResourceAttachment struct {
	connection *persistentPermitConnection
	pin        *persistentOperationPermit
	retired    bool
	draining   bool
	result     error
}

// Stage B only: core tickets are a direct-method simulation, not SQL integration.
// mu is never held over native calls, broker locks, or old permit methods. busy is
// the single-node native reservation; contenders wait with mu released.
type persistentSHMResourceHost struct {
	mu                    sync.Mutex
	cond                  *sync.Cond
	owner                 *persistentPermitOwner
	closing, closed, busy bool
	finalizing            bool
	closeResult           error
	tickets               int
	last                  error
	failAfterMap          bool
	afterDetachStart      func()
	hooks                 shmResourceHooks
	attachments           map[sqlite3.ExperimentalSHMResourceKey]*shmResourceAttachment
	node                  *shmResourceNode
}
type persistentSHMTestTicket struct {
	host     *persistentSHMResourceHost
	released bool
}

func newPersistentSHMResourceHost(o *persistentPermitOwner) *persistentSHMResourceHost {
	h := &persistentSHMResourceHost{owner: o, attachments: make(map[sqlite3.ExperimentalSHMResourceKey]*shmResourceAttachment)}
	h.cond = sync.NewCond(&h.mu)
	return h
}
func (h *persistentSHMResourceHost) enterTestCore(c *persistentPermitConnection, k sqlite3.ExperimentalSHMResourceKey) (*persistentSHMTestTicket, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing || h.last != nil {
		return nil, errors.Join(errPersistentPermitClosed, h.last)
	}
	a := h.attachments[k]
	if a == nil || a.connection != c || a.retired || a.draining {
		return nil, errPersistentPermitForeign
	}
	h.tickets++
	return &persistentSHMTestTicket{host: h}, nil
}
func (t *persistentSHMTestTicket) release() error {
	h := t.host
	h.mu.Lock()
	defer h.mu.Unlock()
	if !t.released {
		t.released = true
		h.tickets--
		h.cond.Broadcast()
	}
	return nil
}
func (h *persistentSHMResourceHost) reserve(allowClosing bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for h.busy {
		h.cond.Wait()
	}
	if h.closed || (!allowClosing && h.closing) || h.last != nil {
		return errors.Join(errPersistentPermitClosed, h.last)
	}
	h.busy = true
	return nil
}
func (h *persistentSHMResourceHost) finish() {
	h.mu.Lock()
	h.busy = false
	h.cond.Broadcast()
	h.mu.Unlock()
}
func (h *persistentSHMResourceHost) poison(e error) {
	h.mu.Lock()
	h.last = errors.Join(h.last, e)
	h.cond.Broadcast()
	h.mu.Unlock()
}
func shmSafeCall(fn func() error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			if e, ok := p.(error); ok {
				err = fmt.Errorf("SHM native hook panic: %w", e)
			} else {
				err = fmt.Errorf("SHM native hook panic: %v", p)
			}
		}
	}()
	return fn()
}
func (h *persistentSHMResourceHost) closeNative(v windows.Handle) error {
	return shmSafeCall(func() error {
		if h.hooks.close != nil {
			return h.hooks.close(v)
		}
		return windows.CloseHandle(v)
	})
}
func (h *persistentSHMResourceHost) attach(c *persistentPermitConnection, k sqlite3.ExperimentalSHMResourceKey) error {
	if c == nil || c.owner != h.owner || k.File == 0 || k.Generation == 0 {
		return errPersistentPermitForeign
	}
	if e := h.reserve(false); e != nil {
		return e
	}
	defer h.finish()
	h.mu.Lock()
	_, exists := h.attachments[k]
	h.mu.Unlock()
	if exists {
		return errPersistentPermitForeign
	}
	pin, e := c.begin(context.Background())
	if e != nil {
		return e
	}
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		return errors.Join(errPersistentPermitClosed, pin.release())
	}
	h.attachments[k] = &shmResourceAttachment{connection: c, pin: pin}
	h.mu.Unlock()
	return nil
}
func (h *persistentSHMResourceHost) openNode() (n *shmResourceNode, err error) {
	n = &shmResourceNode{views: make(map[int32]*shmResourceView)}
	b := h.owner.broker
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.revoked {
		return n, errPersistentPermitClosed
	}
	if e := b.checkLineage(); e != nil {
		return n, e
	}
	parent := b.lineage[len(b.lineage)-1].h
	var meta windows.Handle
	defer func() {
		if meta != 0 && meta != windows.InvalidHandle {
			v := meta
			meta = 0
			n.attemptedMetadata = v
			if e := shmSafeCall(func() error {
				if h.hooks.closeMetadata != nil {
					return h.hooks.closeMetadata(v)
				}
				return windows.CloseHandle(v)
			}); e != nil {
				n.retained = true
				err = errors.Join(err, e)
			}
		}
	}()
	meta, err = persistentOpen(parent, b.names[persistentSHM], false)
	if err != nil {
		return n, err
	}
	before, e := inspect(meta)
	if e != nil {
		return n, e
	}
	if before.ID != b.objects[persistentSHM].id || before.Standard.Directory != 0 {
		return n, errUnsafe
	}
	if e = exactName(meta, b.names[persistentSHM]); e != nil {
		return n, e
	}
	err = shmSafeCall(func() error {
		var e error
		if h.hooks.open != nil {
			n.h, e = h.hooks.open(parent, b.names[persistentSHM])
		} else {
			n.h, e = persistentOpenReadOnlyWAL(parent, b.names[persistentSHM])
		}
		return e
	})
	if err != nil {
		return n, err
	}
	if n.h == 0 || n.h == windows.InvalidHandle {
		return n, errUnsafe
	}
	actual, e := inspect(n.h)
	if e != nil {
		return n, e
	}
	if actual.ID != before.ID || actual.Standard.Directory != 0 {
		return n, errUnsafe
	}
	if e = exactName(n.h, b.names[persistentSHM]); e != nil {
		return n, e
	}
	if e = qualifyReadOnlyWALAccess(n.h); e != nil {
		return n, e
	}
	if e = b.checkLineage(); e != nil {
		return n, e
	}
	return n, nil
}
func shmDMS(h windows.Handle, exclusive bool) error {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	return windows.LockFileEx(h, flags, 0, 1, 0, &windows.Overlapped{Offset: 128})
}
func (h *persistentSHMResourceHost) unlockDMS(v windows.Handle) error {
	return shmSafeCall(func() error {
		if h.hooks.unlock != nil {
			return h.hooks.unlock(v)
		}
		return windows.UnlockFileEx(v, 0, 1, 0, &windows.Overlapped{Offset: 128})
	})
}
func (h *persistentSHMResourceHost) admitDMS(n *shmResourceNode) error {
	e := shmDMS(n.h, true)
	if e == nil {
		n.exclusiveDMS = true
		n.attemptedDMSUnlock = true
		if u := h.unlockDMS(n.h); u != nil {
			n.retained = true
			return errors.Join(errSHMFirstOpener, u)
		}
		n.exclusiveDMS = false
		return errSHMFirstOpener
	}
	if !errors.Is(e, windows.ERROR_LOCK_VIOLATION) {
		return e
	}
	if e = shmDMS(n.h, false); e != nil {
		return e
	}
	n.dms = true
	return nil
}
func (h *persistentSHMResourceHost) cleanupView(v *shmResourceView) error {
	if v.base != 0 {
		base := v.base
		v.base = 0
		v.attemptedBase = base
		e := shmSafeCall(func() error {
			if h.hooks.unmap != nil {
				return h.hooks.unmap(base)
			}
			return windows.UnmapViewOfFile(base)
		})
		if e != nil {
			return e
		}
	}
	if v.mapping != 0 && v.mapping != windows.InvalidHandle {
		m := v.mapping
		v.mapping = 0
		v.attemptedMapping = m
		if e := h.closeNative(m); e != nil {
			return e
		}
	}
	return nil
}
func (h *persistentSHMResourceHost) cleanupNode(n *shmResourceNode) error {
	if n == nil {
		return nil
	}
	if n.retained {
		return errSHMRetained
	}
	for r, v := range n.views {
		if e := h.cleanupView(v); e != nil {
			n.retained = true
			return errors.Join(errSHMRetained, e)
		}
		delete(n.views, r)
	}
	if n.dms {
		n.dms = false
		n.attemptedDMSUnlock = true
		if e := h.unlockDMS(n.h); e != nil {
			n.retained = true
			return errors.Join(errSHMRetained, e)
		}
	}
	if n.h != 0 && n.h != windows.InvalidHandle {
		v := n.h
		n.h = 0
		n.attemptedData = v
		if e := h.closeNative(v); e != nil {
			n.retained = true
			return errors.Join(errSHMRetained, e)
		}
	}
	return nil
}
func (h *persistentSHMResourceHost) mapRegion(c *persistentPermitConnection, k sqlite3.ExperimentalSHMResourceKey, region, size, extend int32) (ptr uintptr, err error) {
	if c == nil || c.owner != h.owner || region < 0 || size != 32768 || extend != 0 {
		return 0, errUnsafe
	}
	if e := h.reserve(false); e != nil {
		return 0, e
	}
	defer h.finish()
	h.mu.Lock()
	a := h.attachments[k]
	valid := a != nil && !a.retired && !a.draining && a.connection == c
	h.mu.Unlock()
	if !valid {
		return 0, errPersistentPermitForeign
	}
	n := h.node
	fresh := n == nil
	if fresh {
		n, err = h.openNode()
		if err == nil {
			err = h.admitDMS(n)
		}
		if err != nil {
			cleanup := h.cleanupNode(n)
			if cleanup != nil {
				h.node = n
				h.poison(errors.Join(err, cleanup))
			}
			return 0, errors.Join(err, cleanup)
		}
	}
	if v := n.views[region]; v != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.closing || h.last != nil || a.retired || a.draining || h.attachments[k] != a {
			return 0, errors.Join(errPersistentPermitClosed, h.last)
		}
		return v.base + v.shift, nil
	}
	v := &shmResourceView{}
	defer func() {
		if err != nil {
			if e := h.cleanupView(v); e != nil {
				n.unpublished = append(n.unpublished, v)
				n.retained = true
				err = errors.Join(err, e)
			}
			if fresh || n.retained {
				e := h.cleanupNode(n)
				err = errors.Join(err, e)
				if e != nil {
					h.node = n
					h.poison(err)
				}
			}
		}
	}()
	actual, e := inspect(n.h)
	if e != nil {
		return 0, e
	}
	offset := uint64(region) * 32768
	end := offset + 32768
	if actual.Standard.Size < 0 || end > uint64(actual.Standard.Size) {
		return 0, errUnsafe
	}
	// Native allocation granularity, not a hardcoded assumed 64 KiB.
	var info struct {
		Processor                          uint32
		PageSize                           uint32
		Minimum, Maximum, Mask             uintptr
		Count, Type, AllocationGranularity uint32
		Level, Revision                    uint16
	}
	windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemInfo").Call(uintptr(unsafe.Pointer(&info)))
	gran := uint64(info.AllocationGranularity)
	if gran == 0 {
		return 0, errUnsafe
	}
	aligned := offset / gran * gran
	shift := offset - aligned
	length := shift + 32768
	if uint64(uintptr(length)) != length {
		return 0, errUnsafe
	}
	v.shift = uintptr(shift)
	v.length = uintptr(length)
	err = shmSafeCall(func() error {
		var e error
		if h.hooks.mapping != nil {
			v.mapping, e = h.hooks.mapping(n.h)
		} else {
			v.mapping, e = windows.CreateFileMapping(n.h, nil, windows.PAGE_READONLY, 0, 0, nil)
		}
		return e
	})
	if err != nil {
		return 0, err
	}
	if v.mapping == 0 || v.mapping == windows.InvalidHandle {
		return 0, errUnsafe
	}
	err = shmSafeCall(func() error {
		var e error
		if h.hooks.view != nil {
			v.base, e = h.hooks.view(v.mapping, aligned, v.length)
		} else {
			v.base, e = windows.MapViewOfFile(v.mapping, windows.FILE_MAP_READ, uint32(aligned>>32), uint32(aligned), v.length)
		}
		return e
	})
	if err != nil {
		return 0, err
	}
	if v.base == 0 {
		return 0, errUnsafe
	}
	if h.hooks.beforePublish != nil {
		if e := shmSafeCall(func() error { h.hooks.beforePublish(); return nil }); e != nil {
			return 0, e
		}
	}
	h.mu.Lock()
	if h.closing || h.last != nil || a.retired || a.draining || h.attachments[k] != a {
		h.mu.Unlock()
		return 0, errPersistentPermitClosed
	}
	if h.failAfterMap {
		h.mu.Unlock()
		return 0, errors.New("injected post-map rollback")
	}
	// This commit under mu is the pointer grant linearization point.
	n.views[region] = v
	h.node = n
	h.mu.Unlock()
	return v.base + v.shift, nil
}
func (h *persistentSHMResourceHost) detach(c *persistentPermitConnection, k sqlite3.ExperimentalSHMResourceKey, remove bool) error {
	h.mu.Lock()
	a := h.attachments[k]
	if a != nil && a.connection == c {
		a.draining = true
	}
	h.mu.Unlock()
	if h.afterDetachStart != nil {
		h.afterDetachStart()
	}
	h.mu.Lock()
	for h.busy || h.tickets > 0 {
		h.cond.Wait()
	}
	if c == nil || c.owner != h.owner {
		h.mu.Unlock()
		return errPersistentPermitForeign
	}
	// Attach may fail before registration. Absent detach owns nothing.
	if a == nil {
		h.mu.Unlock()
		return nil
	}
	if a.connection != c {
		h.mu.Unlock()
		return errPersistentPermitForeign
	}
	if a.retired {
		e := a.result
		h.mu.Unlock()
		return e
	}
	if h.last != nil {
		e := h.last
		h.mu.Unlock()
		return e
	}
	h.busy = true
	others := 0
	for _, v := range h.attachments {
		if !v.retired && v != a {
			others++
		}
	}
	h.mu.Unlock()
	defer h.finish()
	if others == 0 {
		if e := h.cleanupNode(h.node); e != nil {
			h.poison(e)
			return e
		}
		h.node = nil
	}
	e := a.pin.release()
	if remove {
		e = errors.Join(e, errors.New("SHM deletion unsupported"))
	}
	h.mu.Lock()
	a.retired = true
	a.result = e
	h.cond.Broadcast()
	h.mu.Unlock()
	return e
}
func (h *persistentSHMResourceHost) close() error {
	h.mu.Lock()
	h.closing = true
	h.cond.Broadcast()
	for {
		if h.closed {
			e := h.closeResult
			h.mu.Unlock()
			return e
		}
		if h.last != nil {
			e := errors.Join(errSHMRetained, h.last)
			h.mu.Unlock()
			return e
		}
		live := false
		for _, a := range h.attachments {
			if !a.retired {
				live = true
				break
			}
		}
		if !h.finalizing && !live && !h.busy && h.tickets == 0 {
			h.finalizing = true
			break
		}
		h.cond.Wait()
	}
	h.mu.Unlock()
	e := h.owner.close()
	h.mu.Lock()
	h.closeResult = e
	h.closed = true
	h.cond.Broadcast()
	h.mu.Unlock()
	return e
}
