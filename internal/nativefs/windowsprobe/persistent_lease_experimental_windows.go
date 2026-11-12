//go:build windows && nativeprobe_sqlite_lease && nativeprobe_sqlite_identity

package windowsprobe

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/windows"
)

type persistentLeasePurpose uint8

const (
	leaseMain persistentLeasePurpose = iota
	leaseWAL
	leaseSHMMapping
	leaseSHMLock
)

// Retained only so native RED fixtures can distinguish the former stub from
// setup failures. The implementation never returns this sentinel.
var errPersistentLeaseUnimplemented = errors.New("persistent independent connection lease not implemented")
var errPersistentLeaseClosed = errors.New("persistent connection lease closed")
var errPersistentLeaseForeign = errors.New("persistent connection lease foreign authority")

type persistentLeaseHooks struct {
	beforeResourceClose func()
	afterClosing        func()
	openData            func(windows.Handle, string) (windows.Handle, error)
	afterRegister       func() error
	// Applies only to data candidates and published leases. Temporary metadata
	// observations always use real CloseHandle; their errors still poison owner.
	close     func(windows.Handle) error
	afterRead func() error
}
type persistentLeaseEvidence struct {
	Connections, Resources int
	Terminal               error
	Closed                 bool
}
type persistentLeaseOwner struct {
	gate        sync.RWMutex
	closing     atomic.Bool
	closed      bool
	terminal    error
	broker      *persistentBroker
	connections map[*persistentLeaseConnection]struct{}
	resources   map[*persistentFileLease]struct{}
	hooks       persistentLeaseHooks
}
type persistentLeaseConnection struct {
	owner   *persistentLeaseOwner
	closing atomic.Bool
	closed  bool // owner.gate
	result  error
}
type persistentFileLease struct {
	owner      *persistentLeaseOwner
	connection *persistentLeaseConnection
	closing    atomic.Bool
	h          windows.Handle // owner.gate; consumed before any close attempt
	id         idInfo
	closed     bool
	result     error
}

func newPersistentLeaseOwner(root *Root, name string, hooks persistentLeaseHooks) (*persistentLeaseOwner, error) {
	b, e := newPersistentBroker(root, name, persistentHooks{})
	if e != nil {
		return nil, e
	}
	for role := persistentMain; role <= persistentSHM; role++ {
		if e = b.acquireExisting(context.Background(), role); e != nil {
			return nil, errors.Join(e, b.close())
		}
	}
	return &persistentLeaseOwner{broker: b, connections: make(map[*persistentLeaseConnection]struct{}), resources: make(map[*persistentFileLease]struct{}), hooks: hooks}, nil
}

// All checks that read terminal/registry state require gate. closing is atomic
// because the transition must reject late work before its writer lock is granted.
func (o *persistentLeaseOwner) ready() error {
	if o.closing.Load() || o.closed || o.terminal != nil {
		return errors.Join(errPersistentLeaseClosed, o.terminal)
	}
	return nil
}
func (o *persistentLeaseOwner) newConnection(ctx context.Context) (*persistentLeaseConnection, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	o.gate.Lock()
	defer o.gate.Unlock()
	if e := o.ready(); e != nil {
		return nil, e
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	c := &persistentLeaseConnection{owner: o}
	o.connections[c] = struct{}{}
	return c, nil
}
func (c *persistentLeaseConnection) ready() error {
	if e := c.owner.ready(); e != nil {
		return e
	}
	if c.closing.Load() || c.closed {
		return errors.Join(errPersistentLeaseClosed, c.result)
	}
	if _, ok := c.owner.connections[c]; !ok {
		return errPersistentLeaseClosed
	}
	return nil
}
func leasePurposeRole(p persistentLeasePurpose) (persistentRole, error) {
	switch p {
	case leaseMain:
		return persistentMain, nil
	case leaseWAL:
		return persistentWAL, nil
	case leaseSHMMapping, leaseSHMLock:
		return persistentSHM, nil
	default:
		return 0, errUnsafe
	}
}
func (o *persistentLeaseOwner) closeData(h windows.Handle) error {
	if o.hooks.close != nil {
		return o.hooks.close(h)
	}
	return windows.CloseHandle(h)
}
func (c *persistentLeaseConnection) acquire(ctx context.Context, purpose persistentLeasePurpose) (out *persistentFileLease, err error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	o := c.owner
	o.gate.Lock()
	defer o.gate.Unlock()
	if e := c.ready(); e != nil {
		return nil, e
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	role, e := leasePurposeRole(purpose)
	if e != nil {
		return nil, e
	}
	b := o.broker
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.revoked {
		return nil, errors.Join(errPersistentLeaseClosed, b.terminal)
	}
	if e = b.checkLineage(); e != nil {
		return nil, e
	}
	parent := b.lineage[len(b.lineage)-1]
	var meta, candidate windows.Handle
	var registered *persistentFileLease
	// Own every valid handle immediately, including a handle returned with error.
	// Unpublished data is consumed once through closeData; metadata is separately
	// consumed through native CloseHandle. Never publish after cleanup uncertainty.
	defer func() {
		var cleanup error
		if candidate != 0 && candidate != windows.InvalidHandle {
			h := candidate
			candidate = 0
			if registered != nil {
				delete(o.resources, registered)
				registered.h = 0
				registered.closed = true
			}
			cleanup = errors.Join(cleanup, o.closeData(h))
		}
		if meta != 0 && meta != windows.InvalidHandle {
			h := meta
			meta = 0
			cleanup = errors.Join(cleanup, windows.CloseHandle(h))
		}
		if cleanup != nil {
			o.terminal = errors.Join(o.terminal, cleanup)
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
		candidate, err = persistentOpen(parent.h, b.names[role], true)
	}
	if err != nil {
		return nil, err
	}
	if e = ctx.Err(); e != nil {
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
		o.terminal = errors.Join(o.terminal, e)
		return nil, e
	}
	if e = c.ready(); e != nil {
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	registered = &persistentFileLease{owner: o, connection: c, h: candidate, id: actual.ID}
	o.resources[registered] = struct{}{}
	if o.hooks.afterRegister != nil {
		if e = o.hooks.afterRegister(); e != nil {
			return nil, e
		}
	}
	if e = c.ready(); e != nil {
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	candidate = 0 // registry now exclusively owns the published resource
	return registered, nil
}

// Trusted, non-reentrant callback only. The handle must not escape. Defer unlock
// also runs on panic; the panic is deliberately not recovered or replaced here.
func (c *persistentLeaseConnection) borrow(ctx context.Context, l *persistentFileLease, fn func(windows.Handle) error) error {
	if l == nil || l.owner != c.owner || l.connection != c {
		return errPersistentLeaseForeign
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	o := c.owner
	o.gate.RLock()
	defer o.gate.RUnlock()
	if e := c.ready(); e != nil {
		return e
	}
	if l.closing.Load() || l.closed || l.h == 0 {
		return errors.Join(errPersistentLeaseClosed, l.result)
	}
	if _, ok := o.resources[l]; !ok {
		return errPersistentLeaseClosed
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	if fn == nil {
		return errUnsafe
	}
	return fn(l.h)
}

// Called with the owner's writer gate held, after admitted borrows drained.
func (l *persistentFileLease) consume() error {
	if l.closed {
		return l.result
	}
	l.closing.Store(true)
	l.closed = true
	h := l.h
	l.h = 0
	delete(l.owner.resources, l)
	if h != 0 {
		l.result = l.owner.closeData(h)
	}
	if l.result != nil {
		l.owner.terminal = errors.Join(l.owner.terminal, l.result)
	}
	return l.result
}
func (l *persistentFileLease) close() error {
	if l.closing.CompareAndSwap(false, true) && l.owner.hooks.beforeResourceClose != nil {
		l.owner.hooks.beforeResourceClose()
	}
	o := l.owner
	o.gate.Lock()
	defer o.gate.Unlock()
	return l.consume()
}
func (c *persistentLeaseConnection) close() error {
	c.closing.Store(true)
	o := c.owner
	o.gate.Lock()
	defer o.gate.Unlock()
	if c.closed {
		return c.result
	}
	c.closed = true
	for l := range o.resources {
		if l.connection == c {
			c.result = errors.Join(c.result, l.consume())
		}
	}
	delete(o.connections, c)
	return c.result
}
func (o *persistentLeaseOwner) close() error {
	if o.closing.CompareAndSwap(false, true) && o.hooks.afterClosing != nil {
		o.hooks.afterClosing()
	}
	o.gate.Lock()
	defer o.gate.Unlock()
	if o.closed {
		return o.terminal
	}
	o.closed = true
	for l := range o.resources {
		_ = l.consume()
	}
	for c := range o.connections {
		c.closing.Store(true)
		c.closed = true
		c.result = errors.Join(c.result, o.terminal)
		delete(o.connections, c)
	}
	// Connection/resource retirement never calls this: retained DB/WAL/SHM
	// protections last for the entire owner lifetime, not the last SQL lease.
	o.terminal = errors.Join(o.terminal, o.broker.close())
	return o.terminal
}
func (o *persistentLeaseOwner) readWorkspace(ctx context.Context, r *Root, parts []string) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	o.gate.RLock()
	defer o.gate.RUnlock()
	if e := o.ready(); e != nil {
		return nil, e
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	data, e := o.broker.readWorkspace(ctx, r, parts)
	if e != nil {
		return nil, e
	}
	if o.hooks.afterRead != nil {
		if e = o.hooks.afterRead(); e != nil {
			return nil, e
		}
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	// This operation was admitted before close began and is allowed to finish.
	// Owner close cannot consume its identities before these bytes are delivered.
	return data, nil
}
func (o *persistentLeaseOwner) evidence() persistentLeaseEvidence {
	o.gate.RLock()
	defer o.gate.RUnlock()
	return persistentLeaseEvidence{Connections: len(o.connections), Resources: len(o.resources), Terminal: o.terminal, Closed: o.closing.Load() || o.closed}
}
