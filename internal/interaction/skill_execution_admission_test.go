package interaction

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func skillRunSetup(t *testing.T) (*Store, SkillRunIntent) {
	t.Helper()
	if !platformFileCapability().Enabled {
		t.Skip("native files unavailable")
	}
	s, c := setup(t)
	if _, err := s.BindWorkspace(c, "s", WorkspacePolicy{Root: t.TempDir(), Mode: ModePlan}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO interaction_api_sessions(session_id,request_id,binding,mode) VALUES('s','session-create','offline-provider','plan')`); err != nil {
		t.Fatal(err)
	}
	imported := skillDecide(t, s, skillPrepare(t, s, "import", readonlyManifest), "import-confirm", "confirm")
	p, err := s.PrepareSkill(c, "s", SkillIntent{RequestID: "enable", Kind: "enable", CatalogID: imported.CatalogID, RevisionID: imported.RevisionID})
	if err != nil {
		t.Fatal(err)
	}
	skillDecide(t, s, p, "enable-confirm", "confirm")
	return s, SkillRunIntent{RequestID: "preview", CatalogID: imported.CatalogID, RevisionID: imported.RevisionID, ExecutionProfile: SkillRunProfile, Inputs: json.RawMessage(readonlyInputs)}
}
func TestSkillRunAdmissionAtomicOfflineAndIdempotent(t *testing.T) {
	s, in := skillRunSetup(t)
	ctx := t.Context()
	p, err := s.PrepareSkillRun(ctx, "s", in)
	if err != nil {
		t.Fatal(err)
	}
	if p.State != "pending" || p.Review.Eligibility != "ready" || p.RunID != "" {
		t.Fatal(p)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM interaction_runs`).Scan(&n)
	if n != 0 {
		t.Fatal("preview ran", n)
	}
	d := OperatorDecision{RequestID: "confirm", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, e := s.DecideSkillRun(ctx, "s", d)
			if e != nil || a.State != "admitted" {
				t.Error(a, e)
			}
		}()
	}
	wg.Wait()
	a, err := s.SkillRunOperation(ctx, "s", d.RequestID)
	if err != nil || a.RunID == "" {
		t.Fatal(a, err)
	}
	s.db.QueryRow(`SELECT count(*) FROM interaction_runs`).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
	r, err := s.SkillRun(ctx, "s", a.RunID)
	if err != nil || r.Execution != "queued" || r.Evaluation != "not_run" || len(r.Nodes) != 2 {
		t.Fatal(r, err)
	}
	cp, err := s.EngineCheckpoint(ctx, a.RunID)
	if err != nil || cp.Action != skillRunAction || cp.Budgets.MaxSteps != 2 || len(cp.Calls) != 2 {
		t.Fatal(cp, err)
	}
	if _, err = s.SkillRun(ctx, "foreign", a.RunID); err == nil {
		t.Fatal("foreign run")
	}
	if _, err = s.SkillRunOperation(ctx, "foreign", d.RequestID); err == nil {
		t.Fatal("foreign operation")
	}
	in.Inputs = json.RawMessage(`{}`)
	if _, err = s.PrepareSkillRun(ctx, "s", in); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
func TestSkillRunAdmissionSelectionChangesInvalidate(t *testing.T) {
	s, in := skillRunSetup(t)
	ctx := t.Context()
	p, err := s.PrepareSkillRun(ctx, "s", in)
	if err != nil {
		t.Fatal(err)
	}
	off, err := s.PrepareSkill(ctx, "s", SkillIntent{RequestID: "off", Kind: "disable", CatalogID: in.CatalogID})
	if err != nil {
		t.Fatal(err)
	}
	skillDecide(t, s, off, "off-confirm", "confirm")
	a, err := s.DecideSkillRun(ctx, "s", OperatorDecision{RequestID: "confirm", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
	if err != nil || a.State != "stale" || a.RunID != "" {
		t.Fatal(a, err)
	}
}
func TestSkillRunAdmissionPendingLimits(t *testing.T) {
	s, in := skillRunSetup(t)
	ctx := t.Context()
	for i := 0; i < 16; i++ {
		in.RequestID = string(rune('a' + i))
		if _, err := s.PrepareSkillRun(ctx, "s", in); err != nil {
			t.Fatal(i, err)
		}
	}
	in.RequestID = "overflow"
	if _, err := s.PrepareSkillRun(ctx, "s", in); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
}
func TestSkillRunMalformedRawInputRejectedBeforeFingerprint(t *testing.T) {
	s, in := skillRunSetup(t)
	for _, raw := range []string{`{broken`, `null`, `{"first":{},"first":{}}`} {
		in.Inputs = json.RawMessage(raw)
		if _, err := s.PrepareSkillRun(t.Context(), "s", in); !errors.Is(err, ErrInvalid) {
			t.Fatal(raw, err)
		}
	}
}
func TestSkillRunOperationLifetimeAndByteCaps(t *testing.T) {
	for _, test := range []string{"count", "bytes", "requests"} {
		t.Run(test, func(t *testing.T) {
			s, in := skillRunSetup(t)
			p, e := s.PrepareSkillRun(t.Context(), "s", in)
			if e != nil {
				t.Fatal(e)
			}
			tx, e := s.db.BeginTx(t.Context(), nil)
			if e != nil {
				t.Fatal(e)
			}
			switch test {
			case "bytes":
				// Immutable reservations cannot be rewritten; seed another fully reserved
				// retained operation, as if a larger prior preview already consumed the pool.
				_, e = tx.Exec(`INSERT INTO interaction_skill_run_operations SELECT 'big',session_id,preview,inputs,67108864-reserved_bytes,'stale',result FROM interaction_skill_run_operations WHERE id=?`, p.OperationID)
			case "count":
				for i := 1; i < 1024 && e == nil; i++ {
					_, e = tx.Exec(`INSERT INTO interaction_skill_run_operations SELECT ?,session_id,preview,inputs,reserved_bytes,'stale',result FROM interaction_skill_run_operations WHERE id=?`, fmt.Sprintf("seed-%d", i), p.OperationID)
				}
			case "requests":
				for i := 1; i < 2048 && e == nil; i++ {
					_, e = tx.Exec(`INSERT INTO interaction_skill_run_requests SELECT ?,session_id,operation_id,fingerprint,result FROM interaction_skill_run_requests LIMIT 1`, fmt.Sprintf("seed-%d", i))
				}
			}
			if e != nil {
				tx.Rollback()
				t.Fatal(e)
			}
			if e = tx.Commit(); e != nil {
				t.Fatal(e)
			}
			in.RequestID = "overflow"
			if _, e = s.PrepareSkillRun(t.Context(), "s", in); !errors.Is(e, ErrBudget) {
				t.Fatal(test, e)
			}
		})
	}
}
