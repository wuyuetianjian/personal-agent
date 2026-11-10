package interaction

import (
	"agent/internal/storage"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func setup(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, e := storage.OpenSQLite(ctx, filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	if e = storage.Migrate(ctx, db.SQL); e != nil {
		t.Fatal(e)
	}
	s := NewStore(db.SQL)
	if e = s.CreateSession(ctx, "s"); e != nil {
		t.Fatal(e)
	}
	return s, ctx
}
func TestCreateRunConcurrent(t *testing.T) {
	s, c := setup(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for _, id := range []string{"a", "b"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, e := s.CreateRun(c, "s", id, Message{Content: "hello"})
			mu.Lock()
			defer mu.Unlock()
			if e == nil {
				successes++
			} else if !errors.Is(e, ErrConflict) {
				t.Error(e)
			}
		}(id)
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("successes %d", successes)
	}
}
func TestCreateRunIdempotent(t *testing.T) {
	s, c := setup(t)
	a, e := s.CreateRun(c, "s", "a", Message{Content: "hello"})
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.CreateRun(c, "s", "a", Message{Content: "different"})
	if e != nil || a.ID != b.ID {
		t.Fatalf("%+v %v", b, e)
	}
	es, e := s.Events(c, "s", 0, 10)
	if e != nil || len(es) != 1 {
		t.Fatalf("%v %v", es, e)
	}
}
func TestTransitionTerminal(t *testing.T) {
	s, c := setup(t)
	r, e := s.CreateRun(c, "s", "a", Message{})
	if e != nil {
		t.Fatal(e)
	}
	r, e = s.Transition(c, r.ID, r.Version, RunCancelled)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Transition(c, r.ID, r.Version, RunCompleted); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	es, e := s.Events(c, "s", 1, 10)
	if e != nil || len(es) != 1 || es[0].Sequence != 2 || es[0].Version != 2 {
		t.Fatalf("%+v %v", es, e)
	}
}
func TestEventsRollback(t *testing.T) {
	s, c := setup(t)
	r, e := s.CreateRun(c, "s", "a", Message{})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.db.Exec("CREATE TRIGGER fail_event BEFORE INSERT ON interaction_events BEGIN SELECT RAISE(ABORT, 'fixture'); END")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Transition(c, r.ID, r.Version, RunCompleted); e == nil {
		t.Fatal("expected event failure")
	}
	got, e := s.GetRun(c, r.ID)
	if e != nil || got.State != RunRunning || got.Version != 1 {
		t.Fatalf("%+v %v", got, e)
	}
	if _, e = s.db.Exec("DROP TRIGGER fail_event"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Transition(c, r.ID, 1, RunCompleted); e != nil {
		t.Fatal(e)
	}
	events, e := s.Events(c, "s", 0, 10)
	if e != nil || len(events) != 2 || events[1].Sequence != 2 {
		t.Fatalf("%+v %v", events, e)
	}
}
func TestApprovalIntegrity(t *testing.T) {
	s, c := setup(t)
	r, e := s.CreateRun(c, "s", "a", Message{})
	if e != nil {
		t.Fatal(e)
	}
	a := Approval{ID: "approval", RunID: r.ID, ActionDigest: "digest", ExpiresAt: time.Now().Add(time.Hour)}
	if e = s.CreateApproval(c, a); e != nil {
		t.Fatal(e)
	}
	if e = s.DecideApproval(c, a.ID, "changed", "approved"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for _, d := range []string{"approved", "denied"} {
		wg.Add(1)
		go func(d string) {
			defer wg.Done()
			e := s.DecideApproval(c, a.ID, "digest", d)
			mu.Lock()
			defer mu.Unlock()
			if e == nil {
				success++
			} else if !errors.Is(e, ErrConflict) {
				t.Error(e)
			}
		}(d)
	}
	wg.Wait()
	if success != 1 {
		t.Fatal(success)
	}
	got, e := s.GetApproval(c, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	if got.Decision == "approved" {
		if e = s.ConsumeApproval(c, a.ID, "digest"); e != nil {
			t.Fatal(e)
		}
	} else if e = s.ConsumeApproval(c, a.ID, "digest"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e = s.ConsumeApproval(c, a.ID, "digest"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e = s.db.Exec("UPDATE interaction_approvals SET action_digest='changed' WHERE id=?", a.ID); e == nil {
		t.Fatal("mutable digest")
	}
}
func TestApprovalExpiredAndConsumptionCAS(t *testing.T) {
	s, c := setup(t)
	r, e := s.CreateRun(c, "s", "a", Message{})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CreateApproval(c, Approval{ID: "expired", RunID: r.ID, ActionDigest: "d", ExpiresAt: time.Now().Add(-time.Second)}); e != nil {
		t.Fatal(e)
	}
	if e = s.DecideApproval(c, "expired", "d", "approved"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e = s.CreateApproval(c, Approval{ID: "live", RunID: r.ID, ActionDigest: "d", ExpiresAt: time.Now().Add(time.Hour)}); e != nil {
		t.Fatal(e)
	}
	if e = s.DecideApproval(c, "live", "d", "approved"); e != nil {
		t.Fatal(e)
	}
	if e = s.ConsumeApproval(c, "live", "wrong"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	n := 0
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := s.ConsumeApproval(c, "live", "d")
			mu.Lock()
			defer mu.Unlock()
			if e == nil {
				n++
			} else if !errors.Is(e, ErrConflict) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if n != 1 {
		t.Fatal(n)
	}
	got, e := s.GetApproval(c, "live")
	if e != nil || got.ConsumedAt == nil {
		t.Fatalf("%+v %v", got, e)
	}
}

// WaitCount establishes that the operation actually queued before expiry.
func TestApprovalQueuedAcrossExpiry(t *testing.T) {
	for _, consume := range []bool{false, true} {
		name := "decision"
		if consume {
			name = "consume"
		}
		t.Run(name, func(t *testing.T) {
			s, c := setup(t)
			r, e := s.CreateRun(c, "s", "a", Message{})
			if e != nil {
				t.Fatal(e)
			}
			expiry := time.Now().Add(300 * time.Millisecond)
			if e = s.CreateApproval(c, Approval{ID: "a", RunID: r.ID, ActionDigest: "d", ExpiresAt: expiry}); e != nil {
				t.Fatal(e)
			}
			if consume {
				if e = s.DecideApproval(c, "a", "d", "approved"); e != nil {
					t.Fatal(e)
				}
			}
			tx, e := s.db.BeginTx(c, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			before := s.db.Stats().WaitCount
			done := make(chan error, 1)
			go func() {
				if consume {
					done <- s.ConsumeApproval(c, "a", "d")
				} else {
					done <- s.DecideApproval(c, "a", "d", "approved")
				}
			}()
			deadline := time.Now().Add(time.Second)
			for s.db.Stats().WaitCount == before && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if s.db.Stats().WaitCount == before {
				t.Fatal("operation did not queue")
			}
			if !time.Now().Before(expiry) {
				t.Fatal("fixture failed to queue before expiry")
			}
			time.Sleep(time.Until(expiry) + 20*time.Millisecond)
			if e = tx.Rollback(); e != nil {
				t.Fatal(e)
			}
			if e = <-done; !errors.Is(e, ErrConflict) {
				t.Fatalf("expired %s accepted: %v", name, e)
			}
			got, e := s.GetApproval(c, "a")
			if e != nil {
				t.Fatal(e)
			}
			want := "pending"
			if consume {
				want = "approved"
			}
			if got.Decision != want || got.ConsumedAt != nil {
				t.Fatalf("expired approval mutated: %+v", got)
			}
		})
	}
}
func TestApprovalConsumeRejectsExpiredOrTerminal(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		name := "expired"
		if terminal {
			name = "terminal"
		}
		t.Run(name, func(t *testing.T) {
			s, c := setup(t)
			r, e := s.CreateRun(c, "s", "a", Message{})
			if e != nil {
				t.Fatal(e)
			}
			expiry := time.Now().Add(100 * time.Millisecond)
			if terminal {
				expiry = time.Now().Add(time.Hour)
			}
			if e = s.CreateApproval(c, Approval{ID: "a", RunID: r.ID, ActionDigest: "d", ExpiresAt: expiry}); e != nil {
				t.Fatal(e)
			}
			if e = s.DecideApproval(c, "a", "d", "approved"); e != nil {
				t.Fatal(e)
			}
			if terminal {
				if _, e = s.Transition(c, r.ID, r.Version, RunCancelled); e != nil {
					t.Fatal(e)
				}
			} else {
				time.Sleep(time.Until(expiry) + time.Millisecond)
			}
			if e = s.ConsumeApproval(c, "a", "d"); !errors.Is(e, ErrConflict) {
				t.Fatal(e)
			}
			a, e := s.GetApproval(c, "a")
			if e != nil || a.ConsumedAt != nil {
				t.Fatalf("%+v %v", a, e)
			}
		})
	}
}
