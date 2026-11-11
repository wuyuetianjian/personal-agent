// Package rootlease is a disconnected lifecycle experiment. It provides no
// exported construction/admission API and no DB exclusion or native backend.
package rootlease

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"sync"
)

type backend interface {
	read(context.Context, []string) ([]byte, error)
	close() error
}
type opener func(context.Context, string) (backend, error)
type binding struct {
	reference, path string
	scope, policy   []byte
}
type registry struct {
	mu                            sync.Mutex
	cond                          *sync.Cond
	stopping, done                chan struct{}
	closed                        bool
	closeErr                      error
	capacity, opening, operations int
	epoch                         string
	entropy                       io.Reader
	open                          opener
	entries                       map[string]*prepared
	// Tombstones prevent a nonce collision reviving an aborted reference. Bound
	// total issuance independently of live capacity; a new host starts a new epoch.
	issued map[string]bool
}
type prepared struct {
	cleanupDone      chan struct{}
	owner            *registry
	ref, path        string
	root             backend
	active, disposed bool
	bound            binding
	closeErr         error
}

var errUnavailable = errors.New("rootlease: unavailable authority")

const maxIssued = 4096

func newRegistry(capacity int, open opener) (*registry, error) {
	return newRegistryWithEntropy(capacity, open, rand.Reader)
}
func newRegistryWithEntropy(capacity int, open opener, entropy io.Reader) (*registry, error) {
	if capacity < 1 || capacity > 128 || open == nil || entropy == nil {
		return nil, errUnavailable
	}
	var nonce [32]byte
	if _, err := io.ReadFull(entropy, nonce[:]); err != nil {
		return nil, err
	}
	r := &registry{capacity: capacity, open: open, entropy: entropy, epoch: hex.EncodeToString(nonce[:]), entries: make(map[string]*prepared), issued: make(map[string]bool), stopping: make(chan struct{}), done: make(chan struct{})}
	r.cond = sync.NewCond(&r.mu)
	return r, nil
}
func (r *registry) acquire(ctx context.Context, path string) (*prepared, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.closed || len(r.entries)+r.opening >= r.capacity || len(r.issued) >= maxIssued {
		r.mu.Unlock()
		return nil, errUnavailable
	}
	// Serialize entropy use and reserve a never-before-issued reference before
	// starting an open. Cancellation cannot abandon a running native acquisition.
	ref := ""
	for range 4 {
		var nonce [32]byte
		if _, err := io.ReadFull(r.entropy, nonce[:]); err != nil {
			r.mu.Unlock()
			return nil, err
		}
		candidate := "winlease:v1:" + r.epoch + ":" + hex.EncodeToString(nonce[:])
		if !r.issued[candidate] {
			ref = candidate
			break
		}
	}
	if ref == "" {
		r.mu.Unlock()
		return nil, errUnavailable
	}
	r.issued[ref] = true
	r.opening++
	r.mu.Unlock()
	root, err := r.open(ctx, path)
	if root == nil && err == nil {
		err = errUnavailable
	}
	r.mu.Lock()
	if r.closed {
		err = errors.Join(err, errUnavailable)
	}
	err = errors.Join(err, ctx.Err())
	if err != nil {
		// Keep the opening reservation through cleanup, including a backend returned
		// alongside an error. Shutdown must not return before that cleanup finishes.
		r.mu.Unlock()
		var cleanupErr error
		if root != nil {
			cleanupErr = root.close()
			err = errors.Join(err, cleanupErr)
		}
		r.mu.Lock()
		r.closeErr = errors.Join(r.closeErr, cleanupErr)
		r.opening--
		r.cond.Broadcast()
		r.mu.Unlock()
		return nil, err
	}
	p := &prepared{owner: r, ref: ref, path: path, root: root, cleanupDone: make(chan struct{})}
	r.entries[ref] = p
	r.opening--
	r.cond.Broadcast()
	r.mu.Unlock()
	return p, nil
}
func (p *prepared) reference() string { return p.ref }
func (p *prepared) activate(b binding) error {
	r := p.owner
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || p.disposed || p.active || r.entries[p.ref] != p || b.reference != p.ref || b.path != p.path {
		return errUnavailable
	}
	p.bound = b
	p.bound.scope = bytes.Clone(b.scope)
	p.bound.policy = bytes.Clone(b.policy)
	p.active = true
	return nil
}
func (p *prepared) abort() error {
	r := p.owner
	r.mu.Lock()
	if p.active {
		r.mu.Unlock()
		return nil
	}
	if r.closed {
		r.mu.Unlock()
		<-r.done
		return p.closeErr
	}
	if p.disposed {
		r.mu.Unlock()
		<-p.cleanupDone
		return p.closeErr
	}
	p.disposed = true
	r.operations++
	r.mu.Unlock()
	err := p.root.close()
	r.mu.Lock()
	delete(r.entries, p.ref)
	p.closeErr = err
	close(p.cleanupDone)
	r.closeErr = errors.Join(r.closeErr, err)
	r.operations--
	r.cond.Broadcast()
	r.mu.Unlock()
	return err
}
func (r *registry) read(ctx context.Context, b binding, parts []string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	p := r.entries[b.reference]
	if r.closed || p == nil || !p.active || p.disposed || p.bound.path != b.path || !bytes.Equal(p.bound.scope, b.scope) || !bytes.Equal(p.bound.policy, b.policy) {
		r.mu.Unlock()
		return nil, errUnavailable
	}
	r.operations++
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.operations--; r.cond.Broadcast(); r.mu.Unlock() }()
	data, err := p.root.read(ctx, append([]string(nil), parts...))
	if err = errors.Join(err, ctx.Err()); err != nil {
		return nil, err
	}
	return data, nil
}
func (r *registry) close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		<-r.done
		return r.closeErr
	}
	r.closed = true
	close(r.stopping)
	for r.opening != 0 || r.operations != 0 {
		r.cond.Wait()
	}
	pending := make([]*prepared, 0, len(r.entries))
	for _, p := range r.entries {
		p.disposed = true
		pending = append(pending, p)
	}
	clear(r.entries)
	r.mu.Unlock()
	var result error
	for _, p := range pending {
		p.closeErr = p.root.close()
		close(p.cleanupDone)
		result = errors.Join(result, p.closeErr)
	}
	r.mu.Lock()
	r.closeErr = errors.Join(r.closeErr, result)
	close(r.done)
	r.mu.Unlock()
	return r.closeErr
}
