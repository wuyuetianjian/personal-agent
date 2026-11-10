package interaction

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
)

func queueSetup(t *testing.T) (*Store, func(string, string) QueueItem) {
	s, c := setup(t)
	if e := s.BindChatOnly(c, "s", ChatOnlyPolicy{}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(`INSERT INTO interaction_api_sessions(session_id,request_id,binding,mode) VALUES('s','create','provider-snapshot','chat_only')`); e != nil {
		t.Fatal(e)
	}
	return s, func(req, text string) QueueItem {
		q, e := s.Enqueue(c, "s", req, Message{Content: text})
		if e != nil {
			t.Fatal(e)
		}
		return q
	}
}
func TestQueueFIFOAtomicClaimAndIdempotency(t *testing.T) {
	s, enqueue := queueSetup(t)
	c := t.Context()
	a := enqueue("a", "exact first")
	b := enqueue("b", "second")
	if again := enqueue("a", "exact first"); again.ID != a.ID {
		t.Fatal("duplicate entry")
	}
	if _, e := s.Enqueue(c, "s", "a", Message{Content: "changed"}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e := s.CreateRun(c, "s", "bypass", Message{Content: "bypass"}); !errors.Is(e, ErrConflict) {
		t.Fatal("queue bypass", e)
	}
	r, e := s.AdmitQueue(c, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	if r.Message.Content != "exact first" {
		t.Fatal(r)
	}
	if _, e = s.AdmitQueue(c, a.ID); !errors.Is(e, ErrConflict) {
		t.Fatal("second claim", e)
	}
	if _, e = s.AdmitQueue(c, b.ID); !errors.Is(e, ErrConflict) {
		t.Fatal("concurrent same session", e)
	}
	if _, e = s.WithdrawQueue(c, "s", a.ID, "wa"); !errors.Is(e, ErrConflict) {
		t.Fatal("withdraw admitted", e)
	}
	if _, e = s.Transition(c, r.ID, r.Version, RunCompleted); e != nil {
		t.Fatal(e)
	}
	r2, e := s.AdmitQueue(c, b.ID)
	if e != nil || r.ID == r2.ID {
		t.Fatal(r2, e)
	}
	list, e := s.Queue(c, "s")
	if e != nil || list.Items[0].RunID != r.ID || list.Items[1].RunID != r2.ID {
		t.Fatal(list, e)
	}
}
func TestQueueWithdrawRaceAndCapacity(t *testing.T) {
	s, enqueue := queueSetup(t)
	c := t.Context()
	a := enqueue("a", "first")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); _, e := s.AdmitQueue(c, a.ID); results <- e }()
	go func() { defer wg.Done(); _, e := s.WithdrawQueue(c, "s", a.ID, "withdraw"); results <- e }()
	wg.Wait()
	close(results)
	wins := 0
	for e := range results {
		if e == nil {
			wins++
		} else if !errors.Is(e, ErrConflict) {
			t.Fatal(e)
		}
	}
	if wins != 1 {
		t.Fatal(wins)
	}
	for i := 0; i < QueueSessionLimit; i++ {
		enqueue(string(rune('A'+i)), "pending")
	}
	if _, e := s.Enqueue(c, "s", "overflow", Message{Content: "no"}); !errors.Is(e, ErrQueueFull) {
		t.Fatal(e)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM interaction_queue WHERE request_id='overflow'`).Scan(&n)
	if n != 0 {
		t.Fatal("persisted overflow")
	}
}
func TestQueueClaimRollbackAndLedgerRecovery(t *testing.T) {
	s, enqueue := queueSetup(t)
	c := t.Context()
	a := enqueue("a", "exact")
	s.db.Exec(`CREATE TRIGGER fail_claim BEFORE UPDATE OF state ON interaction_queue WHEN NEW.state='admitted' BEGIN SELECT RAISE(ABORT,'crash'); END`)
	if _, e := s.AdmitQueue(c, a.ID); e == nil {
		t.Fatal("expected failure")
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM interaction_runs`).Scan(&n)
	if n != 0 {
		t.Fatal("orphan run", n)
	}
	s.db.Exec(`DROP TRIGGER fail_claim`)
	r, e := s.AdmitQueue(c, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	reopened := NewStore(s.db)
	if _, e = reopened.AdmitQueue(c, a.ID); !errors.Is(e, ErrConflict) {
		t.Fatal("replayed", e)
	}
	op, e := reopened.QueueOperation(c, "s", "a")
	if e != nil || op.Item.ID != a.ID || op.Item.RunID != r.ID {
		t.Fatal(op, e)
	}
}

func TestQueueChangedRootRevokesAdmission(t *testing.T) {
	s, c := setup(t)
	root := t.TempDir() + "/root"
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := s.BindWorkspace(c, "s", WorkspacePolicy{Root: root, Mode: ModePlan}); e != nil {
		t.Fatal(e)
	}
	s.db.Exec(`INSERT INTO interaction_api_sessions(session_id,request_id,binding,mode) VALUES('s','create','binding','plan')`)
	q, e := s.Enqueue(c, "s", "q", Message{Content: "original root only"})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(root, root+"-old"); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AdmitQueue(c, q.ID); e == nil {
		t.Fatal("replaced root accepted")
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM interaction_runs`).Scan(&n)
	if n != 0 {
		t.Fatal("revoked admission persisted")
	}
}
func TestQueueBlocksContextMutationUntilWithdrawn(t *testing.T) {
	s, enqueue := queueSetup(t)
	q := enqueue("a", "queued")
	c := t.Context()
	if _, e := s.CompactSession(c, "s", SessionOperation{RequestID: "compact", Kind: "compact", ExpectedCursor: 0}); !errors.Is(e, ErrConflict) {
		t.Fatal("pending context changed", e)
	}
	a, e := s.WithdrawQueue(c, "s", q.ID, "withdraw")
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.WithdrawQueue(c, "s", q.ID, "withdraw")
	if e != nil || a.ID != b.ID {
		t.Fatal(e)
	}
	if _, e = s.Enqueue(c, "s", "withdraw", Message{Content: "different operation"}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}

func TestQueueGlobalLimitRejectsBeforePersistence(t *testing.T) {
	s, c := setup(t)
	for session := 0; session < 9; session++ {
		id := fmt.Sprintf("session%d", session)
		if e := s.CreateSession(c, id); e != nil {
			t.Fatal(e)
		}
		if e := s.BindChatOnly(c, id, ChatOnlyPolicy{}); e != nil {
			t.Fatal(e)
		}
		if _, e := s.db.Exec("INSERT INTO interaction_api_sessions(session_id,request_id,binding,mode) VALUES(?,?,?,'chat_only')", id, id, "binding"); e != nil {
			t.Fatal(e)
		}
		for n := 0; n < 16; n++ {
			_, e := s.Enqueue(c, id, fmt.Sprintf("%s-%d", id, n), Message{Content: "bounded"})
			if session < 8 && e != nil {
				t.Fatal(e)
			}
			if session == 8 && !errors.Is(e, ErrQueueFull) {
				t.Fatal(e)
			}
		}
	}
	var count int
	s.db.QueryRow("SELECT count(*) FROM interaction_queue").Scan(&count)
	if count != QueueGlobalLimit {
		t.Fatal(count)
	}
}
