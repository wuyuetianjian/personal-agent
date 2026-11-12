//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_lease && nativeprobe_sqlite_permit && nativeprobe_sqlite_existing_wal && nativeprobe_sqlite_shm_resources

package windowsprobe

import (
	"errors"
	sqlite3 "modernc.org/sqlite/lib"
	"sync"
)

var errSHMResourcesUnimplemented = errors.New("private retained SHM mapping not implemented")

// RED skeleton: constructor owns no native resources. Tickets model core use
// only in direct-method tests; they are not SQL/Rows/Stmt integration.
type persistentSHMResourceHost struct {
	mu               sync.Mutex
	owner            *persistentPermitOwner
	closing          bool
	last             error
	failAfterMap     bool
	afterDetachStart func()
}
type persistentSHMTestTicket struct{ host *persistentSHMResourceHost }

func newPersistentSHMResourceHost(o *persistentPermitOwner) *persistentSHMResourceHost {
	return &persistentSHMResourceHost{owner: o}
}
func (h *persistentSHMResourceHost) enterTestCore() (*persistentSHMTestTicket, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing {
		return nil, errPersistentPermitClosed
	}
	return &persistentSHMTestTicket{h}, nil
}
func (t *persistentSHMTestTicket) release() error { return nil }
func (h *persistentSHMResourceHost) mapRegion(c *persistentPermitConnection, k sqlite3.ExperimentalSHMResourceKey, region, size, extend int32) (uintptr, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last = errSHMResourcesUnimplemented
	return 0, h.last
}
func (h *persistentSHMResourceHost) detach(c *persistentPermitConnection, k sqlite3.ExperimentalSHMResourceKey, remove bool) error {
	if h.afterDetachStart != nil {
		h.afterDetachStart()
	}
	return nil
}
func (h *persistentSHMResourceHost) close() error {
	h.mu.Lock()
	h.closing = true
	h.mu.Unlock()
	return nil
}
