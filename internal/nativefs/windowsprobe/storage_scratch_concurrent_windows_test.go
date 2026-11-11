//go:build windows && nativeprobe_sqlite_identity

package windowsprobe

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

type scratchConcurrentResult struct {
	f   *scratchFile
	n   int
	err error
}

func scratchAwait(t *testing.T, ch <-chan scratchConcurrentResult) scratchConcurrentResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent scratch operation timed out")
		return scratchConcurrentResult{}
	}
}
func scratchSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("scratch barrier timed out")
	}
}
func scratchInventoryProof(t *testing.T, r *Root) {
	t.Helper()
	before := captureHandleInventory(t)
	var h windows.Handle
	if e := windows.DuplicateHandle(windows.CurrentProcess(), r.h, windows.CurrentProcess(), &h, 0, false, windows.DUPLICATE_SAME_ACCESS); e != nil {
		t.Fatal(e)
	}
	defer scratchClose(t, &h)
	live := captureHandleInventory(t)
	if before.files[h] || !live.files[h] {
		t.Fatal("PSS failed exact known duplicate positive control")
	}
	scratchClose(t, &h)
	if after := captureHandleInventory(t); !sameDiskHandles(before, after) {
		t.Fatal("PSS positive control failed restoration")
	}
}

// Broken locking must fail the test rather than hang testing cleanup forever.
// Barriers are released by defer before these cleanup callbacks run.
func scratchConcurrentCleanup(t *testing.T, closeFn func() error) {
	t.Helper()
	t.Cleanup(func() {
		result := make(chan error, 1)
		go func() { result <- closeFn() }()
		select {
		case e := <-result:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(10 * time.Second):
			t.Error("cleanup timed out; operation remains unqualified")
		}
	})
}
func scratchConcurrentStore(t *testing.T, r *Root, h scratchHooks) *scratchStore {
	t.Helper()
	s, e := newScratchStore(r, scratchLimits{Files: 4, FileBytes: 16 << 20, TotalBytes: 32 << 20, IOBytes: 1 << 20}, h)
	if s != nil {
		scratchConcurrentCleanup(t, s.Close)
	}
	if e != nil || s == nil {
		t.Fatalf("store: %v", e)
	}
	return s
}
func scratchConcurrentFile(t *testing.T, s *scratchStore) *scratchFile {
	t.Helper()
	f, e := s.Create()
	if f != nil {
		scratchConcurrentCleanup(t, f.Close)
	}
	if e != nil || f == nil {
		t.Fatalf("file: %v", e)
	}
	return f
}
func TestNativeSQLiteTempScratchConcurrentLifecycle(t *testing.T) {
	t.Run("create_revoked_before_publication", func(t *testing.T) {
		dir, source := scratchFixture(t)
		childPath := filepath.Join(dir, "child")
		if e := os.Mkdir(childPath, 0700); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			if e := os.Remove(childPath); e != nil {
				t.Error(e)
			}
		})
		r, e := source.OpenDirectory([]string{"child"})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			if e := r.Close(); e != nil {
				t.Error(e)
			}
		})
		if len(r.ancestors) == 0 {
			t.Fatal("ancestor barrier fixture lacks lineage")
		}
		scratchInventoryProof(t, r)
		baseline := captureHandleInventory(t)
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		// Release on every failure before any cleanup might wait on the barrier.
		defer unblock()
		s := scratchConcurrentStore(t, r, scratchHooks{afterCreate: func(windows.Handle) error { close(entered); <-release; return nil }})
		done := make(chan scratchConcurrentResult, 1)
		go func() { f, e := s.Create(); done <- scratchConcurrentResult{f: f, err: e} }()
		scratchSignal(t, entered)
		parentID := scratchID(t, s.root.h)
		ancestors := make([]idInfo, len(s.root.ancestors))
		for i, h := range s.root.ancestors {
			ancestors[i] = scratchID(t, h)
		}
		closeResult := make(chan scratchConcurrentResult, 1)
		go func() { closeResult <- scratchConcurrentResult{err: s.Close()} }()
		if got := scratchAwait(t, closeResult); !errors.Is(got.err, errScratchBusy) {
			t.Fatalf("inflight create close=%v", got.err)
		}
		if scratchID(t, s.root.h) != parentID {
			t.Fatal("parent prematurely closed")
		}
		for i, h := range s.root.ancestors {
			if scratchID(t, h) != ancestors[i] {
				t.Fatal("ancestor prematurely closed")
			}
		}
		rejected := make(chan scratchConcurrentResult, 1)
		go func() { f, e := s.Create(); rejected <- scratchConcurrentResult{f: f, err: e} }()
		reject := scratchAwait(t, rejected)
		if reject.f != nil {
			scratchConcurrentCleanup(t, reject.f.Close)
		}
		if reject.f != nil || !errors.Is(reject.err, errScratchClosed) {
			t.Fatalf("new create after revoke=%v", reject.err)
		}
		unblock()
		result := scratchAwait(t, done)
		if result.f != nil {
			scratchConcurrentCleanup(t, result.f.Close)
		}
		if result.f != nil || !errors.Is(result.err, errScratchClosed) {
			t.Fatalf("inflight create published: %+v", result)
		}
		if e := s.Close(); e != nil {
			t.Fatal(e)
		}
		if after := captureHandleInventory(t); !sameDiskHandles(baseline, after) {
			t.Fatalf("create revoke disk leak %v -> %v", baseline.files, after.files)
		}
	})
	t.Run("write_serializes_file_close", func(t *testing.T) {
		_, r := scratchFixture(t)
		baseline := captureHandleInventory(t)
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		defer unblock()
		var closes atomic.Int32
		s := scratchConcurrentStore(t, r, scratchHooks{write: func(h windows.Handle, b []byte) (uint32, error) {
			close(entered)
			<-release
			var n uint32
			e := windows.WriteFile(h, b, &n, nil)
			return n, e
		}, close: func(h windows.Handle) error {
			closes.Add(1)
			// Actual handle bytes must survive the serialized write before deletion.
			_, seekErr := windows.Seek(h, 0, 0)
			data := make([]byte, len("serialized-write"))
			var n uint32
			readErr := windows.ReadFile(h, data, &n, nil)
			var mismatch error
			if n != uint32(len(data)) || string(data) != "serialized-write" {
				mismatch = errors.New("close observed incorrect actual write bytes")
			}
			return errors.Join(seekErr, readErr, mismatch, windows.CloseHandle(h))
		}})
		f := scratchConcurrentFile(t, s)
		h := f.h
		id := scratchID(t, h)
		written := make(chan scratchConcurrentResult, 1)
		closed := make(chan scratchConcurrentResult, 1)
		started := make(chan struct{})
		go func() {
			n, e := f.WriteAt([]byte("serialized-write"), 0)
			written <- scratchConcurrentResult{n: n, err: e}
		}()
		scratchSignal(t, entered)
		go func() { close(started); closed <- scratchConcurrentResult{err: f.Close()} }()
		scratchSignal(t, started)
		// A bounded negative observation is not the whole oracle: live handle ID,
		// successful actual write, and one physical close are also required.
		// The attempt signal precedes the call: this is a bounded scheduling
		// experiment, not a scheduler-independent proof of mutual exclusion.
		select {
		case got := <-closed:
			t.Fatalf("close passed blocked write: %v", got.err)
		case <-time.After(50 * time.Millisecond):
		}
		if closes.Load() != 0 || scratchID(t, h) != id {
			t.Fatal("blocked write lost actual file handle")
		}
		unblock()
		w := scratchAwait(t, written)
		c := scratchAwait(t, closed)
		if w.err != nil || w.n != len("serialized-write") || c.err != nil || closes.Load() != 1 {
			t.Fatalf("write=%+v close=%+v count=%d", w, c, closes.Load())
		}
		if e := f.Close(); e != nil || closes.Load() != 1 {
			t.Fatalf("repeated close %v count=%d", e, closes.Load())
		}
		if e := s.Close(); e != nil {
			t.Fatal(e)
		}
		if after := captureHandleInventory(t); !sameDiskHandles(baseline, after) {
			t.Fatal("write/close disk leak")
		}
	})
	t.Run("aggregate_growth_is_atomic", func(t *testing.T) {
		_, r := scratchFixture(t)
		baseline := captureHandleInventory(t)
		entered, release := make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		defer unblock()
		var writes atomic.Int32
		s, e := newScratchStore(r, scratchLimits{Files: 2, FileBytes: 100, TotalBytes: 100, IOBytes: 100}, scratchHooks{write: func(h windows.Handle, b []byte) (uint32, error) {
			call := writes.Add(1)
			// Two synchronous seed writes precede the first growth. Hold that growth
			// after its quota reservation but before its real kernel write.
			if call == 3 {
				close(entered)
				<-release
			}
			var n uint32
			e := windows.WriteFile(h, b, &n, nil)
			return n, e
		}})
		if s != nil {
			scratchConcurrentCleanup(t, s.Close)
		}
		if e != nil {
			t.Fatal(e)
		}
		a, b := scratchConcurrentFile(t, s), scratchConcurrentFile(t, s)
		for _, f := range []*scratchFile{a, b} {
			if n, e := f.WriteAt(bytes.Repeat([]byte{'s'}, 40), 0); e != nil || n != 40 {
				t.Fatalf("seed %d %v", n, e)
			}
		}
		firstResult, secondResult := make(chan scratchConcurrentResult, 1), make(chan scratchConcurrentResult, 1)
		go func() {
			n, e := a.WriteAt(bytes.Repeat([]byte{'g'}, 20), 40)
			firstResult <- scratchConcurrentResult{f: a, n: n, err: e}
		}()
		scratchSignal(t, entered)
		attempted := make(chan struct{})
		go func() {
			close(attempted)
			n, e := b.WriteAt(bytes.Repeat([]byte{'g'}, 20), 40)
			secondResult <- scratchConcurrentResult{f: b, n: n, err: e}
		}()
		scratchSignal(t, attempted)
		// In-flight growth reservation versus a competing attempted call. The
		// pre-call signal plus bounded wait is not an all-schedules race proof.
		var second scratchConcurrentResult
		secondFinished := false
		select {
		case got := <-secondResult:
			if got.n != 0 || !errors.Is(got.err, errScratchLimit) {
				t.Fatalf("competitor succeeded/unknown error while first growth held: %+v", got)
			}
			second = got
			secondFinished = true
		case <-time.After(50 * time.Millisecond):
		}
		if writes.Load() != 3 {
			t.Fatalf("competitor reached kernel-write hook before release: calls=%d", writes.Load())
		}
		// Independently inspect the actual file objects while the adapter lock is
		// held. Neither growth may have reached disk at this barrier.
		for _, f := range []*scratchFile{a, b} {
			var st standardInfo
			if e := query(f.h, windows.FileStandardInfo, &st); e != nil || st.Size != 40 {
				t.Fatalf("growth reached disk before release: size=%d error=%v", st.Size, e)
			}
		}
		unblock()
		first := scratchAwait(t, firstResult)
		if !secondFinished {
			second = scratchAwait(t, secondResult)
		}
		success, refused := 0, 0
		for _, got := range []scratchConcurrentResult{first, second} {
			wantSize := int64(60)
			if got.err == nil && got.n == 20 {
				success++
			} else if errors.Is(got.err, errScratchLimit) && got.n == 0 {
				refused++
				wantSize = 40
			} else {
				t.Fatalf("unexpected grow %+v", got)
			}
			size, e := got.f.Size()
			if e != nil || size != wantSize {
				t.Fatalf("size=%d want=%d error=%v", size, wantSize, e)
			}
			data := make([]byte, int(wantSize))
			if n, e := got.f.ReadAt(data, 0); e != nil || n != len(data) {
				t.Fatalf("read %d %v", n, e)
			}
			want := bytes.Repeat([]byte{'s'}, 40)
			if wantSize == 60 {
				want = append(want, bytes.Repeat([]byte{'g'}, 20)...)
			}
			if !bytes.Equal(data, want) {
				t.Fatalf("actual grow bytes=%x", data)
			}
		}
		if success != 1 || refused != 1 || writes.Load() != 3 {
			t.Fatalf("success=%d refused=%d actualwrites=%d", success, refused, writes.Load())
		}
		s.mu.Lock()
		charged := s.charged
		s.mu.Unlock()
		if charged != 100 {
			t.Fatalf("charge=%d", charged)
		}
		if e := a.Close(); e != nil {
			t.Fatal(e)
		}
		if e := b.Close(); e != nil {
			t.Fatal(e)
		}
		s.mu.Lock()
		charged = s.charged
		s.mu.Unlock()
		if charged != 0 {
			t.Fatalf("charge after close=%d", charged)
		}
		if e := s.Close(); e != nil {
			t.Fatal(e)
		}
		if after := captureHandleInventory(t); !sameDiskHandles(baseline, after) {
			t.Fatal("concurrent quota disk leak")
		}
	})
}
