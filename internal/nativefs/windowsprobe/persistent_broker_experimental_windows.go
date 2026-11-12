//go:build windows && nativeprobe_sqlite_identity

package windowsprobe

import (
	"context"
	"errors"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type persistentRole uint8

const (
	persistentMain persistentRole = iota
	persistentWAL
	persistentSHM
)

var errPersistentPending = errors.New("persistent broker not ready")
var errPersistentClosed = errors.New("persistent broker closed")

type persistentHooks struct {
	beforeCreate    func(persistentRole) error
	afterCreate     func(persistentRole) error
	afterMetadata   func(persistentRole) error
	afterRegistered func(persistentRole) error
	close           func(windows.Handle) error
}
type persistentEvidence struct {
	Published int
	Closed    bool
	Terminal  error
}
type persistentObject struct {
	h  windows.Handle
	id idInfo
}

// All admission, registration and cleanup serialize under mu. No handle escapes.
// The broker owns lineage independently of the source Root. Lock order: broker,
// then caller workspace Root. No method takes the source Root after construction.
type persistentBroker struct {
	mu                sync.Mutex
	lineage           []persistentObject
	names             [3]string
	objects           [3]persistentObject
	hooks             persistentHooks
	revoked, disposed bool
	terminal          error
}

func newPersistentBroker(source *Root, basename string, hooks persistentHooks) (out *persistentBroker, err error) {
	names := [3]string{basename, basename + "-wal", basename + "-shm"}
	if err = validateNames(names[:], false); err != nil {
		return nil, err
	}
	if source == nil {
		return nil, errUnsafe
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.h == 0 {
		return nil, errPersistentClosed
	}
	b := &persistentBroker{names: names, hooks: hooks}
	// A failed constructor never admits reads. Every returned handle is consumed
	// exactly once, and uncertain cleanup is included in the construction error.
	defer func() {
		if err != nil {
			err = errors.Join(err, b.close())
		}
	}()
	for _, original := range append(append([]windows.Handle{}, source.ancestors...), source.h) {
		if err = rootAccess(original); err != nil {
			return nil, err
		}
		before, e := inspect(original)
		if e != nil {
			return nil, e
		}
		if before.Standard.Directory == 0 || before.ID.Volume != source.id.Volume {
			return nil, errUnsafe
		}
		var dup windows.Handle
		e = windows.DuplicateHandle(windows.CurrentProcess(), original, windows.CurrentProcess(), &dup, 0, false, windows.DUPLICATE_SAME_ACCESS)
		if dup != 0 && dup != windows.InvalidHandle {
			b.lineage = append(b.lineage, persistentObject{dup, before.ID})
		}
		if e != nil {
			return nil, e
		}
		if dup == 0 || dup == windows.InvalidHandle {
			return nil, errUnsafe
		}
		after, e := inspect(dup)
		if e != nil {
			return nil, e
		}
		if !sameQualifiedGuard(before, after) || after.Standard.Directory == 0 {
			return nil, errUnsafe
		}
	}
	if len(b.lineage) == 0 || b.lineage[len(b.lineage)-1].id != source.id {
		return nil, errUnsafe
	}
	return b, nil
}
func (b *persistentBroker) checkLineage() error {
	for _, o := range b.lineage {
		now, e := inspect(o.h)
		if e != nil {
			return e
		}
		if now.ID != o.id || now.Standard.Directory == 0 {
			return errUnsafe
		}
	}
	return nil
}

// FILE_OPEN is deliberate: no CREATE, overwrite, truncation or delete-on-close.
// Both read and write sharing preserve stock external readers/writers' ability
// to open. This is NOT SQLite's byte-range locking protocol or a snapshot.
func persistentOpen(parent windows.Handle, name string, data bool) (windows.Handle, error) {
	n, e := windows.NewNTUnicodeString(name)
	if e != nil {
		return 0, e
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: parent, ObjectName: n}
	oa.Length = uint32(unsafe.Sizeof(oa))
	access := uint32(windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE)
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	options := uint32(windows.FILE_OPEN_REPARSE_POINT | windows.FILE_OPEN_NO_RECALL | windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if data {
		access |= windows.FILE_READ_DATA | windows.FILE_WRITE_DATA
		share &^= windows.FILE_SHARE_DELETE
		options |= windows.FILE_NON_DIRECTORY_FILE
	}
	var h windows.Handle
	var io windows.IO_STATUS_BLOCK
	e = windows.NtCreateFile(&h, access, &oa, &io, nil, 0, share, windows.FILE_OPEN, options, 0, 0)
	if e == nil && (io.Status != 0 || io.Information != 1 /* FILE_OPENED */ || h == 0 || h == windows.InvalidHandle) {
		e = errUnsafe
	}
	return h, e
}
func (b *persistentBroker) acquireExisting(ctx context.Context, role persistentRole) (err error) {
	if e := ctx.Err(); e != nil {
		return e
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.revoked {
		return errors.Join(errPersistentClosed, b.terminal)
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	if role > persistentSHM || b.objects[role].h != 0 {
		return errUnsafe
	}
	if e := b.checkLineage(); e != nil {
		return e
	}
	parent := b.lineage[len(b.lineage)-1].h
	var meta, candidate windows.Handle
	// Cleanup cannot restore admission after an uncertain result. Consume handle
	// variables first: retrying a numeric value after Close could close a reused one.
	defer func() {
		var cleanup error
		for _, p := range []*windows.Handle{&candidate, &meta} {
			h := *p
			*p = 0
			if h != 0 && h != windows.InvalidHandle {
				cleanup = errors.Join(cleanup, windows.CloseHandle(h))
			}
		}
		if cleanup != nil {
			b.revoked = true
			b.terminal = errors.Join(b.terminal, cleanup)
			err = errors.Join(err, cleanup)
		}
	}()
	meta, err = persistentOpen(parent, b.names[role], false)
	if err != nil {
		return err
	}
	before, e := inspect(meta)
	if e != nil {
		return e
	}
	if before.Standard.Directory != 0 || before.ID.Volume != b.lineage[len(b.lineage)-1].id.Volume {
		return errUnsafe
	}
	if e = exactName(meta, b.names[role]); e != nil {
		return e
	}
	if b.hooks.afterMetadata != nil {
		if e = b.hooks.afterMetadata(role); e != nil {
			return e
		}
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = b.checkLineage(); e != nil {
		return e
	}
	candidate, err = persistentOpen(parent, b.names[role], true)
	if err != nil {
		return err
	}
	actual, e := inspect(candidate)
	if e != nil {
		return e
	}
	if actual.Standard.Directory != 0 || !unchanged(before, actual) {
		return errUnsafe
	}
	if e = exactName(candidate, b.names[role]); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	// Close the metadata observation before publication. Failure means the
	// candidate remains unpublished and this broker is permanently quarantined.
	old := meta
	meta = 0
	if e = windows.CloseHandle(old); e != nil {
		b.revoked = true
		b.terminal = errors.Join(b.terminal, e)
		return e
	}
	b.objects[role] = persistentObject{candidate, actual.ID}
	candidate = 0
	if b.hooks.afterRegistered != nil {
		if e = b.hooks.afterRegistered(role); e != nil {
			b.revoked = true
			b.terminal = errors.Join(b.terminal, e)
			return e
		}
	}
	if e = ctx.Err(); e != nil {
		b.revoked = true
		b.terminal = errors.Join(b.terminal, e)
		return e
	}
	return nil
}
func (b *persistentBroker) readWorkspace(ctx context.Context, root *Root, parts []string) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.revoked {
		return nil, errors.Join(errPersistentClosed, b.terminal)
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if root == nil {
		return nil, errUnsafe
	}
	if e := b.checkLineage(); e != nil {
		return nil, e
	}
	excluded := map[idInfo]struct{}{}
	for _, o := range b.objects {
		if o.h == 0 {
			return nil, errPersistentPending
		}
		current, e := inspect(o.h)
		if e != nil {
			return nil, e
		}
		if current.ID != o.id || current.Standard.Directory != 0 {
			return nil, errUnsafe
		}
		excluded[o.id] = struct{}{}
	}
	// Protection holds for the host lifetime, not an individual SQL transaction.
	// There is deliberately no unprotected fallback after close or failed checks.
	return root.read(ctx, parts, excluded)
}
func (b *persistentBroker) evidence() persistentEvidence {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := persistentEvidence{Closed: b.revoked, Terminal: b.terminal}
	for _, o := range b.objects {
		if o.h != 0 {
			e.Published++
		}
	}
	return e
}
func (b *persistentBroker) close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.revoked = true
	if b.disposed {
		return b.terminal
	}
	b.disposed = true
	closeOwned := func(o *persistentObject) {
		h := o.h
		o.h = 0
		if h == 0 {
			return
		}
		var e error
		if b.hooks.close != nil {
			e = b.hooks.close(h)
		} else {
			e = windows.CloseHandle(h)
		}
		b.terminal = errors.Join(b.terminal, e)
	}
	for i := len(b.objects) - 1; i >= 0; i-- {
		closeOwned(&b.objects[i])
	}
	for i := len(b.lineage) - 1; i >= 0; i-- {
		closeOwned(&b.lineage[i])
	}
	return b.terminal
}

// Creation is initially a refusal stub for native test-first qualification.
// Created means FILE_CREATED was confirmed; Registered means verified identity
// was retained by this broker. A false Created is not an unknown-effect guarantee.
type persistentCreateResult struct{ Created, Registered bool }

var errPersistentCreateUnimplemented = errors.New("persistent sidecar creation not implemented")

func (b *persistentBroker) createMissing(context.Context, persistentRole) (persistentCreateResult, error) {
	return persistentCreateResult{}, errPersistentCreateUnimplemented
}
