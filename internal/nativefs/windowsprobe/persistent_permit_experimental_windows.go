//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_lease && nativeprobe_sqlite_permit

package windowsprobe

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"sync"
)

var errPersistentPermitUnimplemented = errors.New("persistent operation permit owner not implemented")
var errPersistentPermitClosed = errors.New("persistent operation permit closed")
var errPersistentPermitForeign = errors.New("persistent operation permit foreign authority")

type persistentPermitHooks struct {
	openData            func(windows.Handle, string) (windows.Handle, error)
	close               func(windows.Handle) error
	afterClosing        func()
	beforeResourceClose func()
	afterReleaseStart   func()
}

// Internal pointer-only objects: zero values, copying and hand-constructed tokens
// are unsupported. A future opaque-ID bridge must resolve registered bindings.
// All mutable state is protected by mu. Native calls and trusted callbacks
// execute outside mu; operation and resource counts retain their authorities.
type persistentPermitOwner struct {
	permits         map[*persistentOperationPermit]bool
	mu              sync.Mutex
	cond            *sync.Cond
	broker          *persistentBroker
	hooks           persistentPermitHooks
	closing, closed bool
	terminal        error
	connections     map[*persistentPermitConnection]bool
	resources       map[*persistentPermitResource]bool
}
type persistentPermitConnection struct {
	owner           *persistentPermitOwner
	closing, closed bool
	ops             int
	result          error
}
type persistentOperationPermit struct {
	connection    *persistentPermitConnection
	ending, ended bool
	methods       int
	result        error
}
type persistentPermitResource struct {
	owner           *persistentPermitOwner
	connection      *persistentPermitConnection
	purpose         persistentLeasePurpose
	h               windows.Handle
	id              idInfo
	refs            int
	closing, closed bool
	result          error
}

func newPersistentPermitOwner(root *Root, name string, hooks persistentPermitHooks) (*persistentPermitOwner, error) {
	b, e := newPersistentBroker(root, name, persistentHooks{})
	if e != nil {
		return nil, e
	}
	for role := persistentMain; role <= persistentSHM; role++ {
		if e = b.acquireExisting(context.Background(), role); e != nil {
			return nil, errors.Join(e, b.close())
		}
	}
	o := &persistentPermitOwner{broker: b, hooks: hooks, connections: make(map[*persistentPermitConnection]bool), resources: make(map[*persistentPermitResource]bool)}
	o.permits = make(map[*persistentOperationPermit]bool)
	o.cond = sync.NewCond(&o.mu)
	return o, nil
}
func (o *persistentPermitOwner) newConnection(ctx context.Context) (*persistentPermitConnection, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if o.closing || o.closed || o.terminal != nil {
		return nil, errors.Join(errPersistentPermitClosed, o.terminal)
	}
	c := &persistentPermitConnection{owner: o}
	o.connections[c] = true
	return c, nil
}
func (c *persistentPermitConnection) begin(ctx context.Context) (*persistentOperationPermit, error) {
	o := c.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if o.closing || o.closed || o.terminal != nil || c.closing || c.closed || !o.connections[c] {
		return nil, errors.Join(errPersistentPermitClosed, o.terminal)
	}
	c.ops++
	p := &persistentOperationPermit{connection: c}
	o.permits[p] = true
	return p, nil
}

// Existing admitted permits may finish lazy opens during ordinary shutdown.
func (p *persistentOperationPermit) admit(ctx context.Context) error {
	o := p.connection.owner
	if e := ctx.Err(); e != nil {
		return e
	}
	if !o.permits[p] || p.ending || p.ended || o.closed || o.terminal != nil || p.connection.closed {
		return errors.Join(errPersistentPermitClosed, o.terminal)
	}
	p.methods++
	return nil
}
func (p *persistentOperationPermit) done() {
	o := p.connection.owner
	o.mu.Lock()
	p.methods--
	o.cond.Broadcast()
	o.mu.Unlock()
}
func (o *persistentPermitOwner) closeData(h windows.Handle) error {
	if o.hooks.close != nil {
		return o.hooks.close(h)
	}
	return windows.CloseHandle(h)
}
func (o *persistentPermitOwner) poison(e error) {
	if e != nil {
		o.mu.Lock()
		o.terminal = errors.Join(o.terminal, e)
		o.mu.Unlock()
	}
}
func (p *persistentOperationPermit) acquire(ctx context.Context, purpose persistentLeasePurpose) (out *persistentPermitResource, err error) {
	o := p.connection.owner
	o.mu.Lock()
	e := p.admit(ctx)
	o.mu.Unlock()
	if e != nil {
		return nil, e
	}
	defer p.done()
	role, e := leasePurposeRole(purpose)
	if e != nil {
		return nil, e
	}
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

func (p *persistentOperationPermit) borrow(ctx context.Context, r *persistentPermitResource, fn func(windows.Handle) error) error {
	o := p.connection.owner
	if r == nil || r.owner != o || r.connection != p.connection {
		return errPersistentPermitForeign
	}
	if fn == nil {
		return errUnsafe
	}
	o.mu.Lock()
	if e := p.admit(ctx); e != nil {
		o.mu.Unlock()
		return e
	}
	if r.closing || r.closed || !o.resources[r] {
		p.methods--
		o.cond.Broadcast()
		e := errors.Join(errPersistentPermitClosed, r.result)
		o.mu.Unlock()
		return e
	}
	r.refs++
	h := r.h
	o.mu.Unlock()
	defer func() { o.mu.Lock(); r.refs--; p.methods--; o.cond.Broadcast(); o.mu.Unlock() }()
	return fn(h)
}
func (p *persistentOperationPermit) release() error {
	o := p.connection.owner
	o.mu.Lock()
	if p.ending {
		for !p.ended {
			o.cond.Wait()
		}
		e := p.result
		o.mu.Unlock()
		return e
	}
	p.ending = true
	o.mu.Unlock()
	if o.hooks.afterReleaseStart != nil {
		o.hooks.afterReleaseStart()
	}
	o.mu.Lock()
	for p.methods != 0 {
		o.cond.Wait()
	}
	p.ended = true
	delete(o.permits, p)
	p.connection.ops--
	p.result = o.terminal
	o.cond.Broadcast()
	e := p.result
	o.mu.Unlock()
	return e
}
func (r *persistentPermitResource) close() error {
	o := r.owner
	o.mu.Lock()
	if r.closing {
		for !r.closed {
			o.cond.Wait()
		}
		e := r.result
		o.mu.Unlock()
		return e
	}
	r.closing = true
	o.mu.Unlock()
	if o.hooks.beforeResourceClose != nil {
		o.hooks.beforeResourceClose()
	}
	o.mu.Lock()
	for r.refs != 0 {
		o.cond.Wait()
	}
	h := r.h
	r.h = 0
	o.mu.Unlock()
	var e error
	if h != 0 && h != windows.InvalidHandle {
		e = o.closeData(h)
	}
	o.mu.Lock()
	r.result = e
	r.closed = true
	delete(o.resources, r)
	o.terminal = errors.Join(o.terminal, e)
	o.cond.Broadcast()
	o.mu.Unlock()
	return e
}
func (c *persistentPermitConnection) close() error {
	o := c.owner
	o.mu.Lock()
	if c.closing {
		for !c.closed {
			o.cond.Wait()
		}
		e := c.result
		o.mu.Unlock()
		return e
	}
	c.closing = true
	for c.ops != 0 {
		o.cond.Wait()
	}
	var rs []*persistentPermitResource
	for r := range o.resources {
		if r.connection == c {
			rs = append(rs, r)
		}
	}
	o.mu.Unlock()
	var e error
	for _, r := range rs {
		e = errors.Join(e, r.close())
	}
	o.mu.Lock()
	c.closed = true
	c.result = e
	delete(o.connections, c)
	o.cond.Broadcast()
	o.mu.Unlock()
	return e
}
func (o *persistentPermitOwner) close() error {
	o.mu.Lock()
	if o.closing {
		for !o.closed {
			o.cond.Wait()
		}
		e := o.terminal
		o.mu.Unlock()
		return e
	}
	o.closing = true
	o.mu.Unlock()
	if o.hooks.afterClosing != nil {
		o.hooks.afterClosing()
	}
	o.mu.Lock()
	var cs []*persistentPermitConnection
	for c := range o.connections {
		cs = append(cs, c)
	}
	o.mu.Unlock()
	for _, c := range cs {
		_ = c.close()
	}
	e := o.broker.close()
	o.mu.Lock()
	o.terminal = errors.Join(o.terminal, e)
	o.closed = true
	o.cond.Broadcast()
	e = o.terminal
	o.mu.Unlock()
	return e
}
