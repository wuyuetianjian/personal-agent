//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_lease && nativeprobe_sqlite_permit && nativeprobe_sqlite_existing_wal

package windowsprobe

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"sync"
	"unsafe"
)

const leaseWALReadOnly persistentLeasePurpose = 4

var errExistingWALReadOnlyUnimplemented = errors.New("fresh readonly WAL resource not implemented")

// This experiment wraps trusted fault hooks before the owner is published.
// Old permit owners and their lifecycle implementation are not mutated.
var persistentWALReadOnlyOwners sync.Map // unexported provenance; retained for experiment lifetime

func newPersistentWALReadOnlyOwner(root *Root, name string, hooks persistentPermitHooks) (*persistentPermitOwner, error) {
	original := hooks.close
	hooks.close = func(h windows.Handle) (err error) {
		defer func() {
			if p := recover(); p != nil {
				if e, ok := p.(error); ok {
					err = fmt.Errorf("readonly WAL close panic: %w", e)
				} else {
					err = fmt.Errorf("readonly WAL close panic: %v", p)
				}
			}
		}()
		if original != nil {
			return original(h)
		}
		return windows.CloseHandle(h)
	}
	o, e := newPersistentPermitOwner(root, name, hooks)
	if e == nil {
		persistentWALReadOnlyOwners.Store(o, struct{}{})
	}
	return o, e
}
func persistentOpenReadOnlyWAL(parent windows.Handle, name string) (windows.Handle, error) {
	n, e := windows.NewNTUnicodeString(name)
	if e != nil {
		return 0, e
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: parent, ObjectName: n}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var h windows.Handle
	var io windows.IO_STATUS_BLOCK
	e = windows.NtCreateFile(&h, windows.FILE_READ_DATA|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, &oa, &io, nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, windows.FILE_OPEN, windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_NO_RECALL|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_NON_DIRECTORY_FILE, 0, 0)
	if e == nil && (io.Status != 0 || io.Information != 1 || h == 0 || h == windows.InvalidHandle) {
		e = errUnsafe
	}
	return h, e
}
func qualifyReadOnlyWALAccess(h windows.Handle) error {
	var mask uint32
	var io windows.IO_STATUS_BLOCK
	if e := windows.NtQueryInformationFile(h, &io, (*byte)(unsafe.Pointer(&mask)), uint32(unsafe.Sizeof(mask)), 8); e != nil {
		return e
	}
	allowed := uint32(windows.FILE_READ_DATA | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE)
	if io.Status != 0 || io.Information != unsafe.Sizeof(mask) || mask != allowed {
		return errUnsafe
	}
	return nil
}
func (p *persistentOperationPermit) acquireReadOnlyWAL(ctx context.Context) (out *persistentPermitResource, err error) {
	o := p.connection.owner
	if _, ok := persistentWALReadOnlyOwners.Load(o); !ok {
		return nil, errPersistentPermitForeign
	}
	o.mu.Lock()
	e := p.admit(ctx)
	o.mu.Unlock()
	if e != nil {
		return nil, e
	}
	defer p.done()
	role := persistentWAL
	purpose := leaseWALReadOnly
	b := o.broker
	b.mu.Lock()
	defer b.mu.Unlock()
	o.mu.Lock()
	terminal := o.terminal
	o.mu.Unlock()
	if terminal != nil {
		return nil, errors.Join(errPersistentPermitClosed, terminal)
	}
	if b.revoked {
		return nil, errors.Join(errPersistentPermitClosed, b.terminal)
	}
	if e = b.checkLineage(); e != nil {
		return nil, e
	}
	parent := b.lineage[len(b.lineage)-1]
	var meta, candidate windows.Handle

	// Own every valid handle immediately, including a handle returned with error.
	// Unpublished data is consumed once through closeData; metadata is separately
	// consumed through native CloseHandle. Never publish after cleanup uncertainty.
	defer func() {
		var cleanup error
		if candidate != 0 && candidate != windows.InvalidHandle {
			h := candidate
			candidate = 0
			cleanup = errors.Join(cleanup, o.closeData(h))
		}
		if meta != 0 && meta != windows.InvalidHandle {
			h := meta
			meta = 0
			cleanup = errors.Join(cleanup, windows.CloseHandle(h))
		}
		if cleanup != nil {
			o.poison(cleanup)
			err = errors.Join(err, cleanup)
			out = nil
		}
	}()
	meta, err = persistentOpen(parent.h, b.names[role], false)
	if err != nil {
		return nil, err
	}
	before, e := inspect(meta)
	if e != nil {
		return nil, e
	}
	if before.Standard.Directory != 0 || before.ID != b.objects[role].id {
		return nil, errUnsafe
	}
	if e = exactName(meta, b.names[role]); e != nil {
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	if o.hooks.openData != nil {
		candidate, err = o.hooks.openData(parent.h, b.names[role])
	} else {
		candidate, err = persistentOpenReadOnlyWAL(parent.h, b.names[role])
	}
	if err != nil {
		return nil, err
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	if e = qualifyReadOnlyWALAccess(candidate); e != nil {
		return nil, e
	}
	actual, e := inspect(candidate)
	if e != nil {
		return nil, e
	}
	if actual.Standard.Directory != 0 || !unchanged(before, actual) {
		return nil, errUnsafe
	}
	if e = exactName(candidate, b.names[role]); e != nil {
		return nil, e
	}
	if e = b.checkLineage(); e != nil {
		return nil, e
	}
	h := meta
	meta = 0
	if e = windows.CloseHandle(h); e != nil {
		o.poison(e)
		return nil, e
	}
	o.mu.Lock()
	if o.terminal != nil {
		e = errors.Join(errPersistentPermitClosed, o.terminal)
	} else {
		e = ctx.Err()
	}
	if e != nil {
		o.mu.Unlock()
		return nil, e
	}
	out = &persistentPermitResource{owner: o, connection: p.connection, purpose: purpose, h: candidate, id: actual.ID}
	o.resources[out] = true
	candidate = 0
	o.mu.Unlock()
	return out, nil
}
