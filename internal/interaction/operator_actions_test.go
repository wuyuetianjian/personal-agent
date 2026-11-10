package interaction

import (
	"agent/internal/storage"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func operatorSetup(t *testing.T) *Store {
	s, _ := queueSetup(t)
	for _, q := range []string{`INSERT INTO projects VALUES('p','old','private','[]','[]','kept','[]','[]','[]','budget','[]')`, `INSERT INTO notification_inbox(id,project_id,trigger_id,title,body,dedup_key,severity,status,delivery_state,created_at) VALUES('n','p','','title','private body','key','info','unread','inbox',CURRENT_TIMESTAMP)`} {
		if _, e := s.db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	return s
}
func TestOperatorAtomicPreviewDecisionReplay(t *testing.T) {
	s := operatorSetup(t)
	c := t.Context()
	in := OperatorIntent{RequestID: "preview", Kind: "project.rename", TargetID: "p", NewValue: "new <script>"}
	p, e := s.PrepareOperator(c, "s", in)
	if e != nil {
		t.Fatal(e)
	}
	if p.State != "pending" || p.OldValue != "old" || p.NewValue != in.NewValue || p.Digest == "" || p.PolicyDigest == "" {
		t.Fatal(p)
	}
	var name string
	s.db.QueryRow(`SELECT name FROM projects WHERE id='p'`).Scan(&name)
	if name != "old" {
		t.Fatal("preview mutated target")
	}
	again, e := s.PrepareOperator(c, "s", in)
	if e != nil || again != p {
		t.Fatal(again, e)
	}
	d := OperatorDecision{RequestID: "confirm", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"}
	done, e := s.DecideOperator(c, "s", d)
	if e != nil || done.State != "committed" {
		t.Fatal(done, e)
	}
	replay, e := s.DecideOperator(c, "s", d)
	if e != nil || replay != done {
		t.Fatal(replay, e)
	}
	got, e := s.OperatorOperation(c, "s", "confirm")
	if e != nil || got != done {
		t.Fatal(got, e)
	}
	var memory string
	s.db.QueryRow(`SELECT name,memory_scope FROM projects WHERE id='p'`).Scan(&name, &memory)
	if name != in.NewValue || memory != "kept" {
		t.Fatal(name, memory)
	}
	var audits, runs int
	s.db.QueryRow(`SELECT count(*) FROM security_audit_log`).Scan(&audits)
	s.db.QueryRow(`SELECT count(*) FROM interaction_runs`).Scan(&runs)
	if audits != 2 || runs != 0 {
		t.Fatal(audits, runs)
	}
	d.Decision = "cancel"
	if _, e = s.DecideOperator(c, "s", d); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	d.RequestID = "other"
	d.Decision = "confirm"
	if _, e = s.DecideOperator(c, "s", d); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	in.NewValue = "changed"
	if _, e = s.PrepareOperator(c, "s", in); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}
func TestOperatorStaleExpiryCancelNoopAndRollback(t *testing.T) {
	for _, kind := range []string{"stale", "expiry", "cancel", "rollback", "noop", "unsupported"} {
		t.Run(kind, func(t *testing.T) {
			s := operatorSetup(t)
			c := t.Context()
			if kind == "noop" {
				s.db.Exec(`UPDATE notification_inbox SET status='read' WHERE id='n'`)
			}
			if kind == "unsupported" {
				s.db.Exec(`UPDATE notification_inbox SET status='delivered' WHERE id='n'`)
			}
			p, e := s.PrepareOperator(c, "s", OperatorIntent{RequestID: "p", Kind: "notification.mark-read", TargetID: "n", NewValue: "read"})
			if kind == "unsupported" {
				if !errors.Is(e, ErrInvalid) {
					t.Fatal(p, e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			d := OperatorDecision{RequestID: "d", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"}
			want := "committed"
			switch kind {
			case "stale":
				s.db.Exec(`UPDATE notification_inbox SET status='read' WHERE id='n'`)
				want = "rejected"
			case "expiry":
				want = "expired"
			case "cancel":
				d.Decision = "cancel"
				want = "rejected"
			case "noop":
				want = "no_op"
			case "rollback":
				s.db.Exec(`CREATE TEMP TRIGGER fail_operator_audit BEFORE INSERT ON security_audit_log WHEN NEW.status='committed' BEGIN SELECT RAISE(ABORT,'injected'); END`)
			}
			now := time.Now()
			if kind == "expiry" {
				now = time.UnixMilli(p.ExpiresAtMS + 1)
			}
			out, e := s.decideOperator(c, "s", d, now)
			if kind == "rollback" {
				if e == nil {
					t.Fatal("wanted injected rollback")
				}
				var v string
				s.db.QueryRow(`SELECT status FROM notification_inbox WHERE id='n'`).Scan(&v)
				if v != "unread" {
					t.Fatal("partial write", v)
				}
				got, e := s.OperatorOperation(c, "s", "p")
				if e != nil || got.State != "pending" {
					t.Fatal(got, e)
				}
				return
			}
			if e != nil || out.State != want {
				t.Fatal(out, e)
			}
		})
	}
}
func TestOperatorConcurrentAndOwnership(t *testing.T) {
	s := operatorSetup(t)
	c := context.Background()
	p, e := s.PrepareOperator(c, "s", OperatorIntent{RequestID: "p", Kind: "notification.mark-read", TargetID: "n", NewValue: "read"})
	if e != nil {
		t.Fatal(e)
	}
	s.CreateSession(c, "s2")
	s.BindChatOnly(c, "s2", ChatOnlyPolicy{})
	s.db.Exec(`INSERT INTO interaction_api_sessions VALUES('s2','s2','other','chat_only')`)
	if _, e = s.OperatorOperation(c, "s2", "p"); e == nil {
		t.Fatal("cross-owner read")
	}
	d := OperatorDecision{RequestID: "d", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"}
	if _, e = s.DecideOperator(c, "s2", d); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	wrong := d
	wrong.Digest = "bad"
	if _, e = s.DecideOperator(c, "s", wrong); !errors.Is(e, ErrInvalid) && !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := s.DecideOperator(c, "s", d)
			if e != nil || v.State != "committed" {
				t.Error(v, e)
			}
		}()
	}
	wg.Wait()
	var n int
	s.db.QueryRow(`SELECT count(*) FROM security_audit_log WHERE status='committed'`).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
}

func TestOperatorRestartReadOnlyAndBoundedInputs(t *testing.T) {
	s := operatorSetup(t)
	c := t.Context()
	for _, in := range []OperatorIntent{{"bad/id", "project.rename", "p", "new"}, {"empty", "project.rename", "p", " "}, {"other", "project.archive", "p", "new"}, {"unread", "notification.mark-read", "n", "unread"}} {
		if _, e := s.PrepareOperator(c, "s", in); !errors.Is(e, ErrInvalid) {
			t.Fatal(e)
		}
	}
	p, e := s.PrepareOperator(c, "s", OperatorIntent{"p", "project.rename", "p", "new"})
	if e != nil {
		t.Fatal(e)
	}
	fresh := NewStore(s.db)
	p2, e := fresh.OperatorOperation(c, "s", "p")
	if e != nil || p2 != p {
		t.Fatal(p2, e)
	}
	done, e := fresh.DecideOperator(c, "s", OperatorDecision{"d", "p", p.Digest, "confirm"})
	if e != nil {
		t.Fatal(e)
	}
	var before, after int
	s.db.QueryRow(`SELECT total_changes()`).Scan(&before)
	got, e := NewStore(s.db).OperatorOperation(c, "s", "d")
	s.db.QueryRow(`SELECT total_changes()`).Scan(&after)
	if e != nil || got != done || before != after {
		t.Fatal(got, e, before, after)
	}
	expired, e := s.prepareOperator(c, "s", OperatorIntent{"expired", "project.rename", "p", "next"}, time.Now().Add(-time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	got, e = s.OperatorOperation(c, "s", expired.OperationID)
	if e != nil || got.State != "expired" {
		t.Fatal(got, e)
	}
	var state string
	s.db.QueryRow(`SELECT state FROM interaction_operator_operations WHERE id='expired'`).Scan(&state)
	if state != "pending" {
		t.Fatal("read consumed", state)
	}
}

func TestOperatorRejectsUnrecoverableDotIDs(t *testing.T) {
	s := operatorSetup(t)
	for _, id := range []string{".", ".."} {
		if _, e := s.PrepareOperator(t.Context(), "s", OperatorIntent{RequestID: id, Kind: "project.rename", TargetID: "p", NewValue: "new"}); !errors.Is(e, ErrInvalid) {
			t.Fatalf("%q: %v", id, e)
		}
	}
}

func TestOperatorTwoPreviewsAndChangedAuthority(t *testing.T) {
	for _, authority := range []bool{false, true} {
		t.Run(fmt.Sprint(authority), func(t *testing.T) {
			s := operatorSetup(t)
			c := t.Context()
			p, e := s.PrepareOperator(c, "s", OperatorIntent{"p1", "project.rename", "p", "first"})
			if e != nil {
				t.Fatal(e)
			}
			q, e := s.PrepareOperator(c, "s", OperatorIntent{"p2", "project.rename", "p", "second"})
			if e != nil {
				t.Fatal(e)
			}
			if authority {
				if _, e = s.db.Exec(`DROP TRIGGER interaction_api_binding_immutable; UPDATE interaction_api_sessions SET binding='changed' WHERE session_id='s'`); e != nil {
					t.Fatal(e)
				}
			} else {
				if r, e := s.DecideOperator(c, "s", OperatorDecision{"d1", p.OperationID, p.Digest, "confirm"}); e != nil || r.State != "committed" {
					t.Fatal(r, e)
				}
			}
			r, e := s.DecideOperator(c, "s", OperatorDecision{"d2", q.OperationID, q.Digest, "confirm"})
			if e != nil || r.State != "rejected" {
				t.Fatal(r, e)
			}
		})
	}
}
func TestOperatorRealDiskCloseReopen(t *testing.T) {
	c := t.Context()
	path := filepath.Join(t.TempDir(), "operator.db")
	db, e := storage.OpenSQLite(c, path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { db.Close() }()
	if e = storage.Migrate(c, db.SQL); e != nil {
		t.Fatal(e)
	}
	s := NewStore(db.SQL)
	if e = s.CreateSession(c, "s"); e != nil {
		t.Fatal(e)
	}
	if e = s.BindChatOnly(c, "s", ChatOnlyPolicy{}); e != nil {
		t.Fatal(e)
	}
	if _, e = db.SQL.Exec(`INSERT INTO interaction_api_sessions VALUES('s','s','binding','chat_only'); INSERT INTO projects VALUES('p','old','private','[]','[]','kept','[]','[]','[]','budget','[]')`); e != nil {
		t.Fatal(e)
	}
	p, e := s.PrepareOperator(c, "s", OperatorIntent{"p", "project.rename", "p", "new"})
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.DecideOperator(c, "s", OperatorDecision{"d", "p", p.Digest, "confirm"})
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	if e = storage.ValidateSQLiteFile(c, path); e != nil {
		t.Fatal(e)
	}
	db, e = storage.OpenSQLite(c, path)
	if e != nil {
		t.Fatal(e)
	}
	if e = storage.Migrate(c, db.SQL); e != nil {
		t.Fatal(e)
	}
	got, e := NewStore(db.SQL).OperatorOperation(c, "s", "d")
	if e != nil || got != r {
		t.Fatal(got, e)
	}
	var n int
	if e = db.SQL.QueryRow(`SELECT count(*) FROM security_audit_log WHERE status='committed'`).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
}
