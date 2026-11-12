//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_lease && nativeprobe_sqlite_permit && nativeprobe_sqlite_existing_wal && nativeprobe_sqlite_shm_resources && nativeprobe_sqlite_shm_slots

package windowsprobe

import (
	"errors"
	"golang.org/x/sys/windows"
	sqlite3 "modernc.org/sqlite/lib"
	"sync"
	"sync/atomic"
)

var errSHMSlotsUnimplemented = errors.New("private SHM slot locks not implemented")
var errSHMSlotRequest = errors.New("invalid private SHM slot request")

type shmSlotRange struct {
	offset, count              int32
	exclusive, unlockAttempted bool
	uncertain                  bool
}
type shmSlotGrant struct {
	connection       *persistentPermitConnection
	node             *shmResourceNode
	ranges           []*shmSlotRange
	retained, closed bool
	result           error
}
type persistentSHMSlotHost struct {
	afterDrainStart                      func()
	mu                                   sync.Mutex
	resources                            *persistentSHMResourceHost
	last                                 error
	nativeLocks, nativeUnlocks, barriers uint64
	lockNative                           func(windows.Handle, int32, int32, bool) error
	unlock                               func(windows.Handle, int32, int32) error
	closeHandle                          func(windows.Handle) error
	barrierHook                          func() error
	fence                                atomic.Uint64
	grants                               map[sqlite3.ExperimentalSHMResourceKey]*shmSlotGrant
}

func newPersistentSHMSlotHost(h *persistentSHMResourceHost) *persistentSHMSlotHost {
	return &persistentSHMSlotHost{resources: h, grants: make(map[sqlite3.ExperimentalSHMResourceKey]*shmSlotGrant)}
}

// B.busy serializes node and slot native work, across every connection. This
// admission is linearized under B.mu before a detach can mark the key draining.
// No registry mutex is held across broker/native operations.
func (h *persistentSHMSlotHost) begin(c *persistentPermitConnection, k sqlite3.ExperimentalSHMResourceKey) error {
	b := h.resources
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.busy {
		b.cond.Wait()
	}
	a := b.attachments[k]
	if c == nil || c.owner != b.owner || a == nil || a.connection != c || a.retired || a.draining {
		return errPersistentPermitForeign
	}
	if b.closing || b.closed || b.last != nil {
		return errors.Join(errPersistentPermitClosed, b.last)
	}
	b.busy = true
	return nil
}
func (h *persistentSHMSlotHost) poison(e error) {
	h.mu.Lock()
	h.last = errors.Join(h.last, e)
	h.mu.Unlock()
	h.resources.poison(e)
	h.resources.owner.poison(e)
}
func (h *persistentSHMSlotHost) closeNative(v windows.Handle) error {
	return shmSafeCall(func() error {
		if h.closeHandle != nil {
			return h.closeHandle(v)
		}
		return windows.CloseHandle(v)
	})
}
func (h *persistentSHMSlotHost) unlockNative(v windows.Handle, o, n int32) error {
	h.mu.Lock()
	h.nativeUnlocks++
	h.mu.Unlock()
	return shmSafeCall(func() error {
		if h.unlock != nil {
			return h.unlock(v, o, n)
		}
		return windows.UnlockFileEx(v, 0, uint32(n), 0, &windows.Overlapped{Offset: uint32(120 + o)})
	})
}
func (h *persistentSHMSlotHost) grant(c *persistentPermitConnection, k sqlite3.ExperimentalSHMResourceKey) (*shmSlotGrant, error) {
	h.mu.Lock()
	g := h.grants[k]
	h.mu.Unlock()
	if g != nil {
		if g.connection != c || g.closed || g.retained {
			return nil, errors.Join(errPersistentPermitForeign, g.result)
		}
		return g, nil
	}
	// Reuse only the qualified fresh RO open transaction, not B's node handle or
	// DMS admission. openNode owns metadata/result+error and grants no mapping.
	n, e := h.resources.openNode()
	g = &shmSlotGrant{connection: c, node: n}
	if e != nil {
		cleanup := h.cleanup(g)
		if cleanup != nil {
			h.mu.Lock()
			h.grants[k] = g
			h.mu.Unlock()
		}
		e = errors.Join(e, cleanup)
		h.poison(e)
		return nil, e
	}
	h.mu.Lock()
	h.grants[k] = g
	h.mu.Unlock()
	return g, nil
}
func (h *persistentSHMSlotHost) cleanup(g *shmSlotGrant) error {
	if g.closed {
		return g.result
	}
	if g.retained || g.node.retained {
		return errors.Join(errSHMRetained, g.result)
	}
	for _, r := range g.ranges {
		if r.unlockAttempted {
			g.retained = true
			return errSHMRetained
		}
		r.unlockAttempted = true
		if e := h.unlockNative(g.node.h, r.offset, r.count); e != nil {
			g.retained = true
			g.result = errors.Join(errSHMRetained, e)
			return g.result
		}
	}
	g.ranges = nil
	if g.node.h != 0 && g.node.h != windows.InvalidHandle {
		v := g.node.h
		g.node.h = 0
		g.node.attemptedData = v
		if e := h.closeNative(v); e != nil {
			g.retained = true
			g.result = errors.Join(errSHMRetained, e)
			return g.result
		}
	}
	g.closed = true
	return nil
}
func slotOverlap(a, b, n, m int32) bool { return a < b+m && b < a+n }
func (h *persistentSHMSlotHost) lock(c *persistentPermitConnection, k sqlite3.ExperimentalSHMResourceKey, o, n, flags int32) (bool, error) {
	if (flags != 5 && flags != 6 && flags != 9 && flags != 10) || o < 0 || o >= 8 || n < 1 || n > 8-o || ((flags&4) != 0 && n != 1) {
		return false, errSHMSlotRequest
	}
	if e := h.begin(c, k); e != nil {
		return false, e
	}
	defer h.resources.finish()
	h.mu.Lock()
	g := h.grants[k]
	h.mu.Unlock()
	exclusive := flags&8 != 0
	unlock := flags&1 != 0
	if unlock {
		if g == nil {
			return false, nil
		}
		if g.connection != c || g.closed || g.retained {
			return false, errors.Join(errPersistentPermitForeign, g.result)
		}
		index := -1
		overlap := false
		for i, r := range g.ranges {
			if slotOverlap(o, r.offset, n, r.count) {
				overlap = true
			}
			if r.offset == o && r.count == n {
				index = i
			}
		}
		if index < 0 {
			if overlap {
				return false, errSHMSlotRequest
			}
			return false, nil
		}
		r := g.ranges[index]
		if r.exclusive != exclusive || r.unlockAttempted {
			return false, errSHMSlotRequest
		}
		r.unlockAttempted = true
		if e := h.unlockNative(g.node.h, o, n); e != nil {
			g.retained = true
			g.result = errors.Join(errSHMRetained, e)
			h.poison(g.result)
			return false, g.result
		}
		g.ranges = append(g.ranges[:index], g.ranges[index+1:]...)
		return false, nil
	}
	var e error
	g, e = h.grant(c, k)
	if e != nil {
		return false, e
	}
	for _, r := range g.ranges {
		if r.offset == o && r.count == n && !r.exclusive && !exclusive {
			return false, nil
		}
		if slotOverlap(o, r.offset, n, r.count) {
			return true, nil
		}
	}
	nativeFlags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		nativeFlags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	h.mu.Lock()
	h.nativeLocks++
	h.mu.Unlock()
	e = shmSafeCall(func() error {
		if h.lockNative != nil {
			return h.lockNative(g.node.h, o, n, exclusive)
		}
		return windows.LockFileEx(g.node.h, nativeFlags, 0, uint32(n), 0, &windows.Overlapped{Offset: uint32(120 + o)})
	})
	if errors.Is(e, windows.ERROR_LOCK_VIOLATION) {
		return true, nil
	}
	if e != nil {
		// Unexpected failure/panic is not proof of a known-unheld range. Keep an
		// uncertainty record and never try to unlock/reacquire it as recovery.
		g.ranges = append(g.ranges, &shmSlotRange{offset: o, count: n, exclusive: exclusive, uncertain: true})
		g.retained = true
		g.result = errors.Join(errSHMRetained, e)
		h.poison(g.result)
		return false, g.result
	}
	g.ranges = append(g.ranges, &shmSlotRange{offset: o, count: n, exclusive: exclusive})
	return false, nil
}
func (h *persistentSHMSlotHost) barrier(c *persistentPermitConnection, k sqlite3.ExperimentalSHMResourceKey) error {
	if e := h.begin(c, k); e != nil {
		return e
	}
	defer h.resources.finish()
	// Go's atomic operations are sequentially consistent. This is real RMW
	// synchronization, separate from counters and from IPC fixture ordering.
	if h.barrierHook != nil {
		if e := shmSafeCall(h.barrierHook); e != nil {
			return e
		}
	}
	h.fence.Add(1)
	h.mu.Lock()
	h.barriers++
	h.mu.Unlock()
	return nil
}
func (h *persistentSHMSlotHost) detach(c *persistentPermitConnection, k sqlite3.ExperimentalSHMResourceKey) error {
	b := h.resources
	b.mu.Lock()
	a := b.attachments[k]
	if c == nil || c.owner != b.owner || (a != nil && a.connection != c) {
		b.mu.Unlock()
		return errPersistentPermitForeign
	}
	if a == nil {
		b.mu.Unlock()
		return nil
	}
	if a.retired {
		e := a.result
		b.mu.Unlock()
		return e
	}
	a.draining = true
	b.mu.Unlock()
	// Trusted test hook after the admission barrier, before native cleanup.
	if h.afterDrainStart != nil {
		h.afterDrainStart()
	}
	b.mu.Lock()
	for b.busy || b.tickets > 0 {
		b.cond.Wait()
	}
	if b.last != nil {
		e := b.last
		b.mu.Unlock()
		return e
	}
	b.busy = true
	b.mu.Unlock()
	defer b.finish() // caller invokes B.detach only after this reservation ends
	h.mu.Lock()
	g := h.grants[k]
	h.mu.Unlock()
	if g == nil {
		return nil
	}
	if g.connection != c {
		return errPersistentPermitForeign
	}
	if e := h.cleanup(g); e != nil {
		h.poison(e)
		return e
	}
	return nil
}
