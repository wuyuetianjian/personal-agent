//go:build nativeprobe_export

package exportprobe

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) (*registry, *authority, *candidate, *grant) {
	t.Helper()
	r, e := newRegistry(rand.Reader, time.Now)
	must(t, e)
	a, e := r.issueAuthority("test-session/export-to-model")
	must(t, e)
	expiry := time.Now().Add(time.Hour)
	c, e := r.qualify([]byte("synthetic neighbor"), expiry)
	must(t, e)
	g, e := r.authorize(a, c, expiry)
	must(t, e)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		must(t, r.close(ctx))
	})
	return r, a, c, g
}
func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func denied(t *testing.T, b []byte, e error) {
	t.Helper()
	if e == nil || len(b) != 0 {
		t.Fatalf("unexpected authority bytes=%q err=%v", b, e)
	}
}
func TestExportCandidateNeverAuthorizes(t *testing.T) {
	r, e := newRegistry(rand.Reader, time.Now)
	must(t, e)
	a, e := r.issueAuthority("purpose")
	must(t, e)
	c, e := r.qualify([]byte("synthetic unknown"), time.Now().Add(time.Hour))
	must(t, e)
	if c == nil {
		t.Fatal("no candidate")
	}
	b, e := r.read(context.Background(), a, &grant{owner: r, nonce: c.nonce, generation: c.generation}, readHooks{})
	denied(t, b, e)
	must(t, r.close(context.Background()))
	if _, e = r.authorize(a, c, time.Now().Add(time.Hour)); e == nil {
		t.Fatal("closed registry granted")
	}
}
func TestExportExplicitSnapshotIsolation(t *testing.T) {
	r, a, _, g := fixture(t)
	b, e := r.read(context.Background(), a, g, readHooks{})
	must(t, e)
	if string(b) != "synthetic neighbor" {
		t.Fatal("wrong snapshot")
	}
	b[0] = 'X'
	again, e := r.read(context.Background(), a, g, readHooks{})
	must(t, e)
	if string(again) != "synthetic neighbor" {
		t.Fatal("caller mutated registry")
	}
	unknown, e := r.qualify([]byte("other unknown"), time.Now().Add(time.Hour))
	must(t, e)
	if unknown == nil {
		t.Fatal("qualification")
	}
	if len(r.grants) != 1 {
		t.Fatal("qualification implicitly granted")
	}
}
func TestExportOpaqueScopeAndRevocation(t *testing.T) {
	r, a, c, g := fixture(t)
	other, e := r.issueAuthority(a.purpose)
	must(t, e)
	b, e := r.read(context.Background(), other, g, readHooks{})
	denied(t, b, e)
	copied := *a
	b, e = r.read(context.Background(), &copied, g, readHooks{})
	denied(t, b, e)
	fake := *g
	b, e = r.read(context.Background(), a, &fake, readHooks{})
	denied(t, b, e)
	r2, a2, _, _ := fixture(t)
	b, e = r2.read(context.Background(), a2, g, readHooks{})
	denied(t, b, e)
	different, e := r.issueAuthority("other-purpose")
	must(t, e)
	b, e = r.read(context.Background(), different, g, readHooks{})
	denied(t, b, e)
	_, _, foreignCandidate, _ := fixture(t)
	if _, e = r.authorize(a, foreignCandidate, time.Now().Add(time.Minute)); e == nil {
		t.Fatal("foreign candidate authorized")
	}
	copyCandidate := *c
	if _, e = r.authorize(a, &copyCandidate, time.Now().Add(time.Minute)); e == nil {
		t.Fatal("forged candidate authorized")
	}
	must(t, r.revoke(context.Background(), g))
	b, e = r.read(context.Background(), a, g, readHooks{})
	denied(t, b, e)
	must(t, r.revoke(context.Background(), g))
	if _, e = r.issueAuthority(""); e == nil {
		t.Fatal("empty purpose")
	}
}
func TestExportVersionExpiryAndBounds(t *testing.T) {
	now := time.Now()
	r, e := newRegistry(rand.Reader, func() time.Time { return now })
	must(t, e)
	defer r.close(context.Background())
	a, e := r.issueAuthority("bounded")
	must(t, e)
	data := []byte("version one")
	c, e := r.qualify(data, now.Add(time.Minute))
	must(t, e)
	g, e := r.authorize(a, c, now.Add(time.Minute))
	must(t, e)
	copy(data, "version two")
	b, e := r.read(context.Background(), a, g, readHooks{})
	must(t, e)
	if string(b) != "version one" {
		t.Fatal("input buffer followed")
	}
	now = now.Add(time.Minute)
	b, e = r.read(context.Background(), a, g, readHooks{})
	denied(t, b, e)
	if _, e = r.qualify(make([]byte, maxSnapshot+1), now.Add(time.Hour)); !errors.Is(e, errLimit) {
		t.Fatal("snapshot quota", e)
	}
	for i := 1; i < maxAuthorities; i++ {
		_, e = r.issueAuthority("bounded")
		must(t, e)
	}
	if _, e = r.issueAuthority("overflow"); !errors.Is(e, errLimit) {
		t.Fatal("authority quota", e)
	}
	for i := 1; i < maxCandidates; i++ {
		_, e = r.qualify([]byte("x"), now.Add(time.Hour))
		must(t, e)
	}
	if _, e = r.qualify(nil, now.Add(time.Hour)); !errors.Is(e, errLimit) {
		t.Fatal("entry quota", e)
	}
	r3, a3, c3, _ := fixture(t)
	for i := 1; i < maxGrants; i++ {
		_, e = r3.authorize(a3, c3, time.Now().Add(time.Minute))
		must(t, e)
	}
	if _, e = r3.authorize(a3, c3, time.Now().Add(time.Minute)); !errors.Is(e, errLimit) {
		t.Fatal("grant quota", e)
	}
	r4, e := newRegistry(rand.Reader, time.Now)
	must(t, e)
	defer r4.close(context.Background())
	for i := 0; i < maxBytes/maxSnapshot; i++ {
		_, e = r4.qualify(make([]byte, maxSnapshot), time.Now().Add(time.Hour))
		must(t, e)
	}
	if _, e = r4.qualify([]byte("x"), time.Now().Add(time.Hour)); !errors.Is(e, errLimit) {
		t.Fatal("byte quota", e)
	}
	if _, e = newRegistry(bytes.NewReader(nil), time.Now); e == nil {
		t.Fatal("entropy failure accepted")
	}
	collision, e := newRegistry(bytes.NewReader(make([]byte, 128)), time.Now)
	must(t, e)
	if _, e = collision.issueAuthority("nonce collision"); e == nil {
		t.Fatal("epoch nonce collision accepted")
	}
}
func TestExportCommitCancelAndRevoke(t *testing.T) {
	r, a, _, g := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b, e := r.read(ctx, a, g, readHooks{})
	denied(t, b, e)
	entered, release := make(chan struct{}, maxReads), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	type result struct {
		b []byte
		e error
	}
	done := make(chan result, maxReads)
	for i := 0; i < maxReads; i++ {
		go func() {
			b, e := r.read(context.Background(), a, g, readHooks{afterCopy: func() { entered <- struct{}{}; <-release }})
			done <- result{b, e}
		}()
	}
	for i := 0; i < maxReads; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("copy admission blocked")
		}
	}
	b, e = r.read(context.Background(), a, g, readHooks{})
	if !errors.Is(e, errLimit) || len(b) != 0 {
		t.Fatal("read concurrency quota", e)
	}
	revokeCtx, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if e = r.revoke(revokeCtx, g); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("revoke drain", e)
	}
	once.Do(func() { close(release) })
	for i := 0; i < maxReads; i++ {
		select {
		case x := <-done:
			denied(t, x.b, x.e)
		case <-time.After(time.Second):
			t.Fatal("read did not abort")
		}
	}
	must(t, r.revoke(context.Background(), g))
	// A committed result may be delivered after revoke returns: no network claim.
	r2, a2, _, g2 := fixture(t)
	committed, deliver := make(chan struct{}), make(chan struct{})
	var release2 sync.Once
	defer release2.Do(func() { close(deliver) })
	go func() {
		b, e := r2.read(context.Background(), a2, g2, readHooks{afterCommit: func() { close(committed); <-deliver }})
		done <- result{b, e}
	}()
	select {
	case <-committed:
	case <-time.After(time.Second):
		t.Fatal("no commit")
	}
	must(t, r2.revoke(context.Background(), g2))
	release2.Do(func() { close(deliver) })
	select {
	case x := <-done:
		must(t, x.e)
		if string(x.b) != "synthetic neighbor" {
			t.Fatal("committed result changed")
		}
	case <-time.After(time.Second):
		t.Fatal("delivery blocked")
	}
	// Cancellation and expiry are rechecked at commit, not only admission.
	r3, a3, _, g3 := fixture(t)
	during, abort := context.WithCancel(context.Background())
	b, e = r3.read(during, a3, g3, readHooks{afterCopy: abort})
	denied(t, b, e)
	now := time.Now()
	r4, e := newRegistry(rand.Reader, func() time.Time { return now })
	must(t, e)
	a4, e := r4.issueAuthority("expiry")
	must(t, e)
	c4, e := r4.qualify([]byte("x"), now.Add(time.Second))
	must(t, e)
	g4, e := r4.authorize(a4, c4, now.Add(time.Second))
	must(t, e)
	b, e = r4.read(context.Background(), a4, g4, readHooks{afterCopy: func() { now = now.Add(time.Second) }})
	denied(t, b, e)
	must(t, r4.close(context.Background()))
	// Close revokes before waiting; timeout does not discard a copied snapshot
	// while an admitted operation can still reach its commit/abort boundary.
	r5, a5, _, g5 := fixture(t)
	started5, release5 := make(chan struct{}), make(chan struct{})
	var once5 sync.Once
	defer once5.Do(func() { close(release5) })
	go func() {
		b, e := r5.read(context.Background(), a5, g5, readHooks{afterCopy: func() { close(started5); <-release5 }})
		done <- result{b, e}
	}()
	select {
	case <-started5:
	case <-time.After(time.Second):
		t.Fatal("close fixture not admitted")
	}
	closeCtx, cancel5 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel5()
	if e = r5.close(closeCtx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("close drain", e)
	}
	b, e = r5.read(context.Background(), a5, g5, readHooks{})
	denied(t, b, e)
	once5.Do(func() { close(release5) })
	select {
	case x := <-done:
		denied(t, x.b, x.e)
	case <-time.After(time.Second):
		t.Fatal("close pending read stuck")
	}
	must(t, r5.close(context.Background()))

}
func TestExportDisconnectedNativeGate(t *testing.T) {
	f, e := parser.ParseFile(token.NewFileSet(), "probe.go", nil, 0)
	must(t, e)
	for _, i := range f.Imports {
		p := strings.Trim(i.Path.Value, "\"")
		if strings.HasPrefix(p, "agent/") || p == "os" || p == "syscall" || strings.Contains(p, "windows") {
			t.Fatal("native/filesystem dependency", p)
		}
	}
	cmd := exec.Command("go", "list", "-deps", "./cmd/...")
	cmd.Dir = "../../.."
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("production graph %v %s", e, out)
	}
	if bytes.Contains(out, []byte("agent/internal/nativefs/exportprobe")) {
		t.Fatal("experiment reached production")
	}
	raw, e := os.ReadFile("../../interaction/workspace_other.go")
	must(t, e)
	if !bytes.Contains(raw, []byte("unavailableNativeAdapter")) {
		t.Fatal("non-Linux default gate changed")
	}
}
