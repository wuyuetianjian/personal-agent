package interaction

import (
	"agent/internal/model"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func admitTestSkillRun(t *testing.T) (*Store, string) {
	t.Helper()
	s, in := skillRunSetup(t)
	p, e := s.PrepareSkillRun(t.Context(), "s", in)
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.DecideSkillRun(t.Context(), "s", OperatorDecision{RequestID: "confirm", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
	if e != nil {
		t.Fatal(e)
	}
	return s, a.RunID
}
func TestSkillRunExactBudgetCompletesOffline(t *testing.T) {
	s, id := admitTestSkillRun(t)
	w, _ := s.Workspace(t.Context(), "s")
	if e := os.WriteFile(filepath.Join(w.Policy.Root, "README.md"), []byte("install here"), 0600); e != nil {
		t.Fatal(e)
	}
	e, err := NewSkillRunEngine(s, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Run(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	r, err := s.SkillRun(t.Context(), "s", id)
	if err != nil || r.Execution != "completed" || len(r.Nodes) != 2 || r.Nodes[0].Outcome != "success" || r.Nodes[1].Outcome != "success" {
		t.Fatal(r, err)
	}
	cp, _ := s.EngineCheckpoint(t.Context(), id)
	if cp.Steps != 2 {
		t.Fatal(cp)
	}
}
func TestSkillRunKnownFailureStopsDependents(t *testing.T) {
	s, id := admitTestSkillRun(t)
	e, err := NewSkillRunEngine(s, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Run(t.Context(), id); err == nil || errors.Is(err, ErrReconciliation) {
		t.Fatal(err)
	}
	r, err := s.SkillRun(t.Context(), "s", id)
	if err != nil || r.Execution != "failed" || r.Nodes[0].Outcome != "failed" || r.Nodes[1].Outcome != "not_started" {
		t.Fatal(r, err)
	}
}
func TestSkillRunTamperedActionCannotBecomeModelOrUndo(t *testing.T) {
	for _, action := range []string{"", "unknown", "file_undo"} {
		t.Run(action, func(t *testing.T) {
			s, id := admitTestSkillRun(t)
			cp, _ := s.EngineCheckpoint(t.Context(), id)
			cp.Action = action
			raw, _ := json.Marshal(cp)
			s.db.Exec(`UPDATE interaction_engine SET checkpoint=? WHERE run_id=?`, string(raw), id)
			e, _ := NewEngine(s, actionOnlyProvider{}, NewWorkspaceBroker(s), EngineOptions{})
			if err := e.Run(t.Context(), id); !errors.Is(err, ErrReconciliation) {
				t.Fatal(err)
			}
			if err := e.Recover(context.Background(), id); !errors.Is(err, ErrReconciliation) {
				t.Fatal(err)
			}
		})
	}
}
func TestSkillRunMissingCheckpointDoesNotRecreateModel(t *testing.T) {
	s, id := admitTestSkillRun(t)
	s.db.Exec(`DELETE FROM interaction_engine WHERE run_id=?`, id)
	e, _ := NewEngine(s, actionOnlyProvider{}, nil, EngineOptions{})
	if err := e.Run(t.Context(), id); !errors.Is(err, ErrReconciliation) {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM interaction_engine WHERE run_id=?`, id).Scan(&n)
	if n != 0 {
		t.Fatal("inserted fallback")
	}
}

func TestSkillForgedCompletionAndPendingIdentityFailClosed(t *testing.T) {
	for _, attack := range []string{"empty_calls", "budget", "pending_id", "forged_result", "missing_pin"} {
		t.Run(attack, func(t *testing.T) {
			s, id := admitTestSkillRun(t)
			cp, _ := s.EngineCheckpoint(t.Context(), id)
			switch attack {
			case "empty_calls":
				cp.Calls = nil
			case "budget":
				cp.Budgets.MaxOutputBytes = 1 << 30
			case "pending_id":
				b, _ := NewSkillRunBroker(s, id)
				p, e := b.Propose(t.Context(), id, cp.Calls[0])
				if e != nil {
					t.Fatal(e)
				}
				p.ID = "foreign"
				cp.Pending = []ToolProposal{p}
				cp.Calls = cp.Calls[1:]
			case "forged_result":
				raw, _ := json.Marshal(SkillRunNodeResult{NodeID: "first", Capability: "workspace.read", ProposalID: "invented", Outcome: "success", Result: &ToolResult{Content: "invented"}})
				s.db.Exec(`UPDATE interaction_skill_node_results SET result=? WHERE run_id=? AND node_id='first'`, string(raw), id)
				cp.Calls = cp.Calls[1:]
				cp.Steps = 1
			case "missing_pin":
				s.db.Exec(`DROP TRIGGER interaction_skill_runs_retain`)
				s.db.Exec(`DELETE FROM interaction_skill_node_results WHERE run_id=?`, id)
				s.db.Exec(`DELETE FROM interaction_skill_event_keys WHERE run_id=?`, id)
				s.db.Exec(`PRAGMA foreign_keys=OFF`)
				s.db.Exec(`DELETE FROM interaction_skill_runs WHERE run_id=?`, id)
				s.db.Exec(`PRAGMA foreign_keys=ON`)
			}
			raw, _ := json.Marshal(cp)
			s.db.Exec(`UPDATE interaction_engine SET checkpoint=? WHERE run_id=?`, string(raw), id)
			e, _ := NewEngine(s, actionOnlyProvider{}, nil, EngineOptions{})
			if err := e.Run(t.Context(), id); !errors.Is(err, ErrReconciliation) {
				t.Fatal(attack, err)
			}
		})
	}
}
func TestSkillMissingCheckpointStillHasReconciliationRecord(t *testing.T) {
	s, id := admitTestSkillRun(t)
	if _, e := s.db.Exec(`DELETE FROM interaction_engine WHERE run_id=?`, id); e != nil {
		t.Fatal(e)
	}
	r, e := s.SkillRun(t.Context(), "s", id)
	if e != nil || r.Execution != "needs_reconciliation" || r.RunID != id {
		t.Fatal(r, e)
	}
}
func TestSkillRoutingDoesNotReplaceSharedModelEngine(t *testing.T) {
	s, id := admitTestSkillRun(t)
	w, _ := s.Workspace(t.Context(), "s")
	os.WriteFile(filepath.Join(w.Policy.Root, "README.md"), []byte("install evidence"), 0600)
	calls := 0
	p := streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		calls++
		return model.TurnResult{Content: "model answer"}, nil
	})
	e, err := NewEngine(s, p, nil, EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Run(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("skill called model")
	}
	run, err := s.CreateRun(t.Context(), "s", "ordinary-after-skill", Message{Content: "ordinary turn"})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Run(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("model route replaced", calls)
	}
}
func TestSkillActionOnModelIdentityCannotExecuteWithoutPin(t *testing.T) {
	broker := &testBroker{}
	engine, s, id := engineFixture(t, actionOnlyProvider{}, broker)
	run, _ := s.GetRun(t.Context(), id)
	cp := engine.initialCheckpoint(run)
	cp.Action = skillRunAction
	cp.Calls = []ToolCall{{ID: "forged", Name: "read", Arguments: `{"path":"file"}`}}
	raw, _ := json.Marshal(cp)
	if _, e := s.db.Exec(`INSERT INTO interaction_engine(run_id,phase,checkpoint) VALUES(?,?,?)`, id, cp.Phase, string(raw)); e != nil {
		t.Fatal(e)
	}
	if e := engine.Run(t.Context(), id); !errors.Is(e, ErrReconciliation) {
		t.Fatal(e)
	}
	if broker.executions.Load() != 0 {
		t.Fatal("checkpoint action granted execution without immutable skill identity")
	}
}
func TestSkillNativeClaimHonorsPinnedDeadline(t *testing.T) {
	s, id := admitTestSkillRun(t)
	b, _ := NewSkillRunBroker(s, id)
	cp, _ := s.EngineCheckpoint(t.Context(), id)
	p, e := b.Propose(t.Context(), id, cp.Calls[0])
	if e != nil {
		t.Fatal(e)
	}
	pin, _, _, e := skillRunData(t.Context(), s.db, id)
	if e != nil {
		t.Fatal(e)
	}
	pin.DeadlineMS = time.Now().Add(-time.Second).UnixMilli()
	pin.AdmissionTimeMS = pin.DeadlineMS - 120000
	raw, _ := json.Marshal(pin)
	s.db.Exec(`DROP TRIGGER interaction_skill_runs_immutable`)
	if _, e = s.db.Exec(`UPDATE interaction_skill_runs SET pin=?,pin_hash=? WHERE run_id=?`, string(raw), operatorHash(pin), id); e != nil {
		t.Fatal(e)
	}
	// Keep the durable checkpoint internally consistent so this exercises the
	// deadline guard, rather than the earlier checkpoint-integrity refusal.
	cp.Deadline = time.UnixMilli(pin.DeadlineMS)
	cp.Pending, cp.Calls, cp.Phase, cp.Steps = []ToolProposal{p}, cp.Calls[1:], PhaseExecuting, 1
	checkpoint, _ := json.Marshal(cp)
	if _, e = s.db.Exec("UPDATE interaction_engine SET phase=?,checkpoint=? WHERE run_id=?", cp.Phase, string(checkpoint), id); e != nil {
		t.Fatal(e)
	}
	prepared, e := b.native.loadPrepared(t.Context(), p.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = b.native.skillDispatchClaim(t.Context(), prepared); !errors.Is(e, ErrBudget) {
		t.Fatal(e)
	}
	var invoked int
	s.db.QueryRow(`SELECT executed FROM interaction_tool_proposals WHERE id=?`, p.ID).Scan(&invoked)
	if invoked != 0 {
		t.Fatal("expired claim executed")
	}
}
