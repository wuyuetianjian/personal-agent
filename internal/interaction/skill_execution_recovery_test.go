package interaction

import (
	"agent/internal/runtime"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func skillJournalFixture(t *testing.T, s *Store, id string, result ToolResult, complete bool) {
	t.Helper()
	ctx := t.Context()
	b, e := NewSkillRunBroker(s, id)
	if e != nil {
		t.Fatal(e)
	}
	cp, _ := s.EngineCheckpoint(ctx, id)
	p, e := b.Propose(ctx, id, cp.Calls[0])
	if e != nil {
		t.Fatal(e)
	}
	cp.Pending = []ToolProposal{p}
	cp.Calls = cp.Calls[1:]
	cp.Steps = 1
	cp.Phase = PhaseReconciliation
	raw, _ := json.Marshal(cp)
	if _, e = s.db.Exec(`UPDATE interaction_engine SET phase=?,checkpoint=? WHERE run_id=?`, cp.Phase, string(raw), id); e != nil {
		t.Fatal(e)
	}
	j := runtime.SQLiteSideEffectStore{DB: s.db}
	_, _, e = j.Claim(ctx, runtime.SideEffectRecord{IdempotencyKey: effectKey(id, p.ID), WorkflowID: id, NodeID: p.ID, CapabilityID: p.Call.Name})
	if e != nil {
		t.Fatal(e)
	}
	if complete {
		raw, _ = json.Marshal(result)
		if e = j.Complete(ctx, effectKey(id, p.ID), string(raw)); e != nil {
			t.Fatal(e)
		}
	}
}
func TestSkillCompletedJournalReconciliationRecoversOnce(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{true: "failure", false: "success"}[failed], func(t *testing.T) {
			s, id := admitTestSkillRun(t)
			skillJournalFixture(t, s, id, ToolResult{Content: "observed", IsError: failed}, true)
			e, _ := NewEngine(s, actionOnlyProvider{}, nil, EngineOptions{})
			if err := e.Recover(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			r, err := s.SkillRun(t.Context(), "s", id)
			if err != nil || r.Nodes[0].Result == nil || r.Nodes[0].Result.Content != "observed" {
				t.Fatal(r, err)
			}
			if failed && (r.Execution != "failed" || r.Nodes[1].Outcome != "not_started") {
				t.Fatal(r)
			}
			if err = e.Recover(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			var n int
			s.db.QueryRow(`SELECT count(*) FROM interaction_events WHERE run_id=? AND type='tool.recovered'`, id).Scan(&n)
			if n != 1 {
				t.Fatal("repeated recovered event", n)
			}
		})
	}
}
func TestSkillUnfinishedJournalRemainsUnknown(t *testing.T) {
	s, id := admitTestSkillRun(t)
	skillJournalFixture(t, s, id, ToolResult{}, false)
	e, _ := NewEngine(s, actionOnlyProvider{}, nil, EngineOptions{})
	if err := e.Recover(t.Context(), id); !errors.Is(err, ErrReconciliation) {
		t.Fatal(err)
	}
	r, err := s.SkillRun(t.Context(), "s", id)
	if err != nil || r.Nodes[0].Outcome != "unknown" {
		t.Fatal(r, err)
	}
}
func TestSkillRevocationAfterDispatchPreservesReadAndStopsNext(t *testing.T) {
	s, id := admitTestSkillRun(t)
	w, _ := s.Workspace(t.Context(), "s")
	os.WriteFile(filepath.Join(w.Policy.Root, "README.md"), []byte("install evidence"), 0600)
	// Suppress all application signals: mutate durable revocation at the claim.
	s.db.Exec(`CREATE TRIGGER revoke_on_claim AFTER UPDATE OF executed ON interaction_tool_proposals WHEN NEW.executed=1 BEGIN UPDATE interaction_skill_runs SET revoked=1 WHERE run_id=NEW.run_id; END`)
	e, _ := NewSkillRunEngine(s, id)
	_ = e.Run(t.Context(), id)
	r, err := s.SkillRun(t.Context(), "s", id)
	if err != nil || r.Execution != "revoked" || r.Nodes[0].Outcome != "success" || r.Nodes[1].Outcome != "not_started" {
		t.Fatal(r, err)
	}
}
func TestSkillRevocationAtLastResultCannotComplete(t *testing.T) {
	s, id := admitTestSkillRun(t)
	w, _ := s.Workspace(t.Context(), "s")
	os.WriteFile(filepath.Join(w.Policy.Root, "README.md"), []byte("install evidence"), 0600)
	s.db.Exec(`CREATE TRIGGER revoke_on_last_result AFTER UPDATE OF result ON interaction_skill_node_results WHEN NEW.node_id='second' BEGIN UPDATE interaction_skill_runs SET revoked=1 WHERE run_id=NEW.run_id; END`)
	e, _ := NewSkillRunEngine(s, id)
	_ = e.Run(t.Context(), id)
	r, err := s.SkillRun(t.Context(), "s", id)
	if err != nil || r.Execution != "revoked" || r.Nodes[1].Outcome != "success" {
		t.Fatal(r, err)
	}
}
func TestSkillSelectionAwayBackPermanentlyRevokesBeforeRead(t *testing.T) {
	s, id := admitTestSkillRun(t)
	r, _ := s.SkillRun(t.Context(), "s", id)
	b, _ := NewSkillRunBroker(s, id)
	cp, _ := s.EngineCheckpoint(t.Context(), id)
	// The file is absent: proposal must succeed without any read preflight.
	p, e := b.Propose(t.Context(), id, cp.Calls[0])
	if e != nil {
		t.Fatal("proposal read contents", e)
	}
	off, e := s.PrepareSkill(t.Context(), "s", SkillIntent{RequestID: "off", Kind: "disable", CatalogID: r.Pin.CatalogID})
	if e != nil {
		t.Fatal(e)
	}
	skillDecide(t, s, off, "off-d", "confirm")
	on, e := s.PrepareSkill(t.Context(), "s", SkillIntent{RequestID: "on", Kind: "enable", CatalogID: r.Pin.CatalogID, RevisionID: r.Pin.RevisionID})
	if e != nil {
		t.Fatal(e)
	}
	skillDecide(t, s, on, "on-d", "confirm")
	cp.Pending = []ToolProposal{p}
	cp.Calls = cp.Calls[1:]
	cp.Phase = PhaseExecuting
	cp.Steps = 1
	raw, _ := json.Marshal(cp)
	s.db.Exec(`UPDATE interaction_engine SET phase=?,checkpoint=? WHERE run_id=?`, cp.Phase, string(raw), id)
	result, err := b.Execute(t.Context(), p.ID)
	if err != nil || !result.IsError {
		t.Fatal(result, err)
	}
	var invoked int
	s.db.QueryRow(`SELECT executed FROM interaction_tool_proposals WHERE id=?`, p.ID).Scan(&invoked)
	if invoked != 0 {
		t.Fatal("read admitted after revocation")
	}
}
func TestSkillArtifactTamperRefusesBeforeExecution(t *testing.T) {
	s, id := admitTestSkillRun(t)
	r, _ := s.SkillRun(t.Context(), "s", id)
	s.db.Exec(`DROP TRIGGER interaction_skill_revision_immutable`)
	s.db.Exec(`UPDATE interaction_skill_revisions SET raw_artifact=raw_artifact||'# tampered' WHERE id=?`, r.Pin.RevisionID)
	if _, e := NewSkillRunEngine(s, id); !errors.Is(e, ErrReconciliation) {
		t.Fatal(e)
	}
}
func TestSkillRootChangePermanentlyRevokes(t *testing.T) {
	s, id := admitTestSkillRun(t)
	w, _ := s.Workspace(t.Context(), "s")
	if e := os.Rename(w.Policy.Root, w.Policy.Root+"-old"); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(w.Policy.Root, 0700); e != nil {
		t.Fatal(e)
	}
	e, _ := NewSkillRunEngine(s, id)
	_ = e.Run(t.Context(), id)
	r, err := s.SkillRun(t.Context(), "s", id)
	if err != nil || !r.Revoked || r.Execution != "revoked" {
		t.Fatal(r, err)
	}
}
func TestSkillCompletedJournalAfterCheckpointFailureRecovers(t *testing.T) {
	s, id := admitTestSkillRun(t)
	w, _ := s.Workspace(t.Context(), "s")
	os.WriteFile(filepath.Join(w.Policy.Root, "README.md"), []byte("install evidence"), 0600)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_completed_checkpoint BEFORE UPDATE OF checkpoint ON interaction_engine WHEN OLD.phase='executing' AND NEW.phase='running' BEGIN SELECT RAISE(ABORT,'injected checkpoint failure'); END`); err != nil {
		t.Fatal(err)
	}
	e, _ := NewSkillRunEngine(s, id)
	if err := e.Run(t.Context(), id); !errors.Is(err, ErrReconciliation) {
		t.Fatal(err)
	}
	cp, _ := s.EngineCheckpoint(t.Context(), id)
	if cp.Phase != PhaseReconciliation || len(cp.Pending) != 1 {
		t.Fatal(cp)
	}
	s.db.Exec(`DROP TRIGGER fail_completed_checkpoint`)
	if err := e.Recover(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if err := e.Run(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	r, err := s.SkillRun(t.Context(), "s", id)
	if err != nil || r.Execution != "completed" {
		t.Fatal(r, err)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM interaction_tool_proposals WHERE run_id=? AND executed=1`, id).Scan(&n)
	if n != 2 {
		t.Fatal("reexecuted", n)
	}
}
func TestSkillRecoveredOutcomeUsesReservedTerminalEvidence(t *testing.T) {
	s, id := admitTestSkillRun(t)
	skillJournalFixture(t, s, id, ToolResult{Content: "known refusal", IsError: true}, true)
	if _, err := s.db.Exec(`UPDATE interaction_skill_runs SET ordinary_events=512,control_events=60 WHERE run_id=?`, id); err != nil {
		t.Fatal(err)
	}
	e, _ := NewEngine(s, actionOnlyProvider{}, nil, EngineOptions{})
	if err := e.Recover(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	r, err := s.SkillRun(t.Context(), "s", id)
	if err != nil || r.Execution != "failed" || r.Nodes[0].Outcome != "failed" {
		t.Fatal(r, err)
	}
}
func TestSkillUnselectedImportDoesNotRevoke(t *testing.T) {
	s, id := admitTestSkillRun(t)
	skillDecide(t, s, skillPrepare(t, s, "import-unselected", strings.Replace(readonlyManifest, `version: "1"`, `version: "2"`, 1)), "import-unselected-d", "confirm")
	r, err := s.SkillRun(t.Context(), "s", id)
	if err != nil || r.Revoked {
		t.Fatal(r, err)
	}
}
func TestSkillPolicyChangeAfterLastObservationRevokesAtFinish(t *testing.T) {
	s, id := admitTestSkillRun(t)
	w, _ := s.Workspace(t.Context(), "s")
	os.WriteFile(filepath.Join(w.Policy.Root, "README.md"), []byte("install evidence"), 0600)
	s.db.Exec(`DROP TRIGGER interaction_workspace_immutable`)
	if _, e := s.db.Exec(`CREATE TRIGGER change_policy_at_result AFTER UPDATE OF result ON interaction_skill_node_results WHEN NEW.node_id='second' BEGIN UPDATE interaction_workspaces SET root_identity='changed' WHERE session_id='s'; END`); e != nil {
		t.Fatal(e)
	}
	e, _ := NewSkillRunEngine(s, id)
	_ = e.Run(t.Context(), id)
	r, err := s.SkillRun(t.Context(), "s", id)
	if err != nil || r.Execution != "revoked" || r.Nodes[1].Outcome != "success" {
		t.Fatal(r, err)
	}
}
func TestSkillOfflineRevokedQueueTerminalizesWithoutCallback(t *testing.T) {
	s, id := admitTestSkillRun(t)
	r, _ := s.SkillRun(t.Context(), "s", id)
	off, e := s.PrepareSkill(t.Context(), "s", SkillIntent{RequestID: "offline-disable", Kind: "disable", CatalogID: r.Pin.CatalogID})
	if e != nil {
		t.Fatal(e)
	}
	skillDecide(t, s, off, "offline-disable-d", "confirm")
	engine, _ := NewEngine(s, actionOnlyProvider{}, nil, EngineOptions{})
	e = engine.Recover(t.Context(), id)
	if e != nil && !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	r, e = s.SkillRun(t.Context(), "s", id)
	if e != nil || r.Execution != "revoked" {
		t.Fatal(r, e)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM side_effect_records WHERE workflow_id=?`, id).Scan(&n)
	if n != 0 {
		t.Fatal("recovery invoked a tool", n)
	}
}
