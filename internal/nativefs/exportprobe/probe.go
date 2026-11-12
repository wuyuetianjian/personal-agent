//go:build nativeprobe_export

// Package exportprobe is a disconnected synthetic authorization experiment.
// It has no production constructor, filesystem backend or native readiness API.
package exportprobe

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"sync"
	"time"
)

var errDenied = errors.New("exportprobe: denied")
var errLimit = errors.New("exportprobe: quota")

const (
	maxAuthorities = 16
	maxCandidates  = 32
	maxGrants      = 64
	maxSnapshot    = 64 * 1024
	maxBytes       = 1024 * 1024
	maxReads       = 8
)

type authority struct {
	owner      *registry
	purpose    string
	nonce      [32]byte
	generation uint64
}
type candidate struct {
	owner         *registry
	nonce, digest [32]byte
	generation    uint64
}
type grant struct {
	owner      *registry
	nonce      [32]byte
	generation uint64
}
type snapshot struct {
	data    []byte
	expires time.Time
}
type export struct {
	a        *authority
	c        *candidate
	expires  time.Time
	revoked  bool
	inflight int
}
type registry struct {
	mu              sync.Mutex
	changed         chan struct{}
	entropy         io.Reader
	now             func() time.Time
	epoch           [32]byte
	generation      uint64
	authorities     map[*authority]bool
	candidates      map[*candidate]*snapshot
	grants          map[*grant]*export
	used            map[[32]byte]bool
	bytes, inflight int
	closed          bool
}
type readHooks struct{ afterCopy, afterCommit func() }

func newRegistry(entropy io.Reader, now func() time.Time) (*registry, error) {
	if entropy == nil || now == nil {
		return nil, errDenied
	}
	r := &registry{entropy: entropy, now: now, changed: make(chan struct{}), authorities: make(map[*authority]bool), candidates: make(map[*candidate]*snapshot), grants: make(map[*grant]*export), used: make(map[[32]byte]bool)}
	if _, e := io.ReadFull(entropy, r.epoch[:]); e != nil {
		return nil, e
	}
	r.used[r.epoch] = true
	return r, nil
}
func (r *registry) signal() { close(r.changed); r.changed = make(chan struct{}) }

// Called under mu. Rejected issuance consumes no entry but never recycles a
// successfully issued nonce or monotonic generation, even after revocation.
func (r *registry) nonce() ([32]byte, uint64, error) {
	var n [32]byte
	if r.closed || r.generation == ^uint64(0) {
		return n, 0, errDenied
	}
	if _, e := io.ReadFull(r.entropy, n[:]); e != nil {
		return n, 0, e
	}
	if r.used[n] {
		return n, 0, errDenied
	}
	r.used[n] = true
	r.generation++
	return n, r.generation, nil
}
func (r *registry) issueAuthority(purpose string) (*authority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || purpose == "" || len(purpose) > 256 {
		return nil, errDenied
	}
	if len(r.authorities) >= maxAuthorities {
		return nil, errLimit
	}
	n, g, e := r.nonce()
	if e != nil {
		return nil, e
	}
	a := &authority{owner: r, purpose: purpose, nonce: n, generation: g}
	r.authorities[a] = true
	return a, nil
}
func (r *registry) qualify(data []byte, expires time.Time) (*candidate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || !r.now().Before(expires) {
		return nil, errDenied
	}
	if len(data) > maxSnapshot || r.bytes+len(data) > maxBytes || len(r.candidates) >= maxCandidates {
		return nil, errLimit
	}
	n, g, e := r.nonce()
	if e != nil {
		return nil, e
	}
	b := append([]byte(nil), data...)
	c := &candidate{owner: r, nonce: n, generation: g, digest: sha256.Sum256(b)}
	r.candidates[c] = &snapshot{data: b, expires: expires}
	r.bytes += len(b)
	return c, nil
}
func (r *registry) authorize(a *authority, c *candidate, expires time.Time) (*grant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	snap := r.candidates[c]
	if r.closed || a == nil || a.owner != r || !r.authorities[a] || c == nil || c.owner != r || snap == nil || !r.now().Before(expires) || expires.After(snap.expires) {
		return nil, errDenied
	}
	if len(r.grants) >= maxGrants {
		return nil, errLimit
	}
	n, g, e := r.nonce()
	if e != nil {
		return nil, e
	}
	ref := &grant{owner: r, nonce: n, generation: g}
	r.grants[ref] = &export{a: a, c: c, expires: expires}
	return ref, nil
}
func (r *registry) allowed(a *authority, g *grant) (*export, *snapshot, bool) {
	x := r.grants[g]
	if r.closed || a == nil || g == nil || a.owner != r || g.owner != r || !r.authorities[a] || x == nil || x.a != a || x.revoked {
		return nil, nil, false
	}
	snap := r.candidates[x.c]
	now := r.now()
	if snap == nil || !now.Before(x.expires) || !now.Before(snap.expires) {
		return nil, nil, false
	}
	return x, snap, true
}
func (r *registry) read(ctx context.Context, a *authority, g *grant, h readHooks) (out []byte, err error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	r.mu.Lock()
	x, snap, ok := r.allowed(a, g)
	if !ok {
		r.mu.Unlock()
		return nil, errDenied
	}
	if r.inflight >= maxReads {
		r.mu.Unlock()
		return nil, errLimit
	}
	x.inflight++
	r.inflight++
	r.mu.Unlock()
	pending := true
	defer func() {
		if pending {
			r.mu.Lock()
			x.inflight--
			r.inflight--
			r.signal()
			r.mu.Unlock()
		}
	}()
	// Immutable snapshots remain registry-owned until every admitted operation
	// leaves commit/abort, so close cannot discard their backing prematurely.
	data := append([]byte(nil), snap.data...)
	if h.afterCopy != nil {
		h.afterCopy()
	}
	r.mu.Lock()
	_, _, ok = r.allowed(a, g)
	err = ctx.Err()
	if !ok && err == nil {
		err = errDenied
	}
	if err == nil {
		out = data
	} // Commit linearization point.
	x.inflight--
	r.inflight--
	pending = false
	r.signal()
	r.mu.Unlock()
	if err == nil && h.afterCommit != nil {
		h.afterCommit()
	}
	return out, err
}
func (r *registry) revoke(ctx context.Context, g *grant) error {
	r.mu.Lock()
	x := r.grants[g]
	if g == nil || g.owner != r || x == nil {
		r.mu.Unlock()
		return errDenied
	}
	x.revoked = true
	r.signal()
	for x.inflight > 0 {
		ch := r.changed
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
		}
		r.mu.Lock()
	}
	r.mu.Unlock()
	return nil
}
func (r *registry) close(ctx context.Context) error {
	r.mu.Lock()
	r.closed = true
	for _, x := range r.grants {
		x.revoked = true
	}
	r.signal()
	for r.inflight > 0 {
		ch := r.changed
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
		}
		r.mu.Lock()
	}
	for _, snap := range r.candidates {
		snap.data = nil
	}
	r.candidates = make(map[*candidate]*snapshot)
	r.bytes = 0
	r.mu.Unlock()
	return nil
}
