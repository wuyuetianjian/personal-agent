//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_lease && nativeprobe_sqlite_permit && nativeprobe_sqlite_existing_wal && nativeprobe_sqlite_shm_resources && nativeprobe_sqlite_shm_slots

package windowsprobe

import (
	"errors"
	"golang.org/x/sys/windows"
	sqlite3 "modernc.org/sqlite/lib"
	"sync"
)

var errSHMSlotsUnimplemented = errors.New("private SHM slot locks not implemented")

type persistentSHMSlotHost struct {
	afterDrainStart                      func()
	mu                                   sync.Mutex
	resources                            *persistentSHMResourceHost
	last                                 error
	nativeLocks, nativeUnlocks, barriers uint64
	unlock                               func(windows.Handle, int32, int32) error
}

func newPersistentSHMSlotHost(h *persistentSHMResourceHost) *persistentSHMSlotHost {
	return &persistentSHMSlotHost{resources: h}
}
func (h *persistentSHMSlotHost) lock(c *persistentPermitConnection, k sqlite3.ExperimentalSHMResourceKey, offset, count, flags int32) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last = errSHMSlotsUnimplemented
	return false, h.last
}
func (h *persistentSHMSlotHost) barrier(c *persistentPermitConnection, k sqlite3.ExperimentalSHMResourceKey) error {
	return errSHMSlotsUnimplemented
}
func (h *persistentSHMSlotHost) detach(c *persistentPermitConnection, k sqlite3.ExperimentalSHMResourceKey) error {
	return nil
}
