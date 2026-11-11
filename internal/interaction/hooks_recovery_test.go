package interaction

import (
	"agent/internal/model"
	"agent/internal/runtime"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func hookOwnedFile(t *testing.T, s *Store) {
	t.Helper()
	w, e := s.Workspace(t.Context(), "s")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(w.Policy.Root, "README.md"), []byte("install owned"), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestHooksDeniedRecovery(t *testing.T) {
	for _, last := range []bool{false, true} {
		for _, resume := range []bool{false, true} {
			t.Run(fmt.Sprintf("last=%v/resume=%v", last, resume), func(t *testing.T) {
				policy := hookPolicyExample
				if !last {
					policy = strings.Replace(policy, "workspace.search", "workspace.read", 1)
				}
				s, id := admitHookRun(t, policy)
				hookOwnedFile(t, s)
				if _, e := s.db.Exec(`CREATE TRIGGER lost_terminal BEFORE UPDATE OF state ON interaction_runs WHEN NEW.state<>'running' BEGIN SELECT RAISE(ABORT,'fixture'); END`); e != nil {
					t.Fatal(e)
				}
				var providerCalls atomic.Int32
				fallback := &hookForbiddenBroker{}
				e, err := NewEngine(s, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
					providerCalls.Add(1)
					return model.TurnResult{}, ErrInvalid
				}), fallback, EngineOptions{})
				if err != nil {
					t.Fatal(err)
				}
				prefix := 0
				if last {
					prefix = 1
				}
				assertUninvoked := func() {
					t.Helper()
					if providerCalls.Load() != 0 || fallback.proposals.Load() != 0 || fallback.executions.Load() != 0 {
						t.Fatalf("forbidden fallback callback: provider=%d propose=%d execute=%d", providerCalls.Load(), fallback.proposals.Load(), fallback.executions.Load())
					}
					var journals, executed, started int
					if err := s.db.QueryRow(`SELECT count(*) FROM side_effect_records WHERE workflow_id=?`, id).Scan(&journals); err != nil {
						t.Fatal(err)
					}
					if err := s.db.QueryRow(`SELECT coalesce(sum(executed),0) FROM interaction_tool_proposals WHERE run_id=?`, id).Scan(&executed); err != nil {
						t.Fatal(err)
					}
					if err := s.db.QueryRow(`SELECT count(*) FROM interaction_events WHERE run_id=? AND type='tool.started'`, id).Scan(&started); err != nil {
						t.Fatal(err)
					}
					checkpoint, err := s.EngineCheckpoint(t.Context(), id)
					if err != nil {
						t.Fatal(err)
					}
					record, err := s.SkillRunWithHooks(t.Context(), "s", id)
					if err != nil {
						t.Fatal(err)
					}
					denied := record.V2.Nodes[prefix]
					if journals != prefix || executed != prefix || started != prefix || checkpoint.Steps != prefix || denied.Outcome != "not_started" || denied.Reason != "hook_denied" || denied.Result != nil || denied.ProposalID != "" {
						t.Fatalf("denial invoked: prefix=%d journals=%d native=%d started=%d steps=%d node=%+v", prefix, journals, executed, started, checkpoint.Steps, denied)
					}
					for _, node := range record.V2.Nodes[prefix+1:] {
						if node.Outcome != "not_started" || node.Result != nil || node.ProposalID != "" {
							t.Fatal("denied suffix ran", node)
						}
					}
					if prefix == 1 && (record.V2.Nodes[0].Outcome != "success" || record.V2.Nodes[0].Result == nil || !strings.Contains(record.V2.Nodes[0].Result.Content, "install owned")) {
						t.Fatal("authorized prefix missing", record.V2.Nodes[0])
					}
				}
				if err := e.Run(t.Context(), id); !errors.Is(err, ErrHookDenied) {
					t.Fatal(err)
				}
				assertUninvoked()
				before, _ := s.SkillHookReceipts(t.Context(), "s", id)
				cp, _ := s.EngineCheckpoint(t.Context(), id)
				s.db.Exec(`DROP TRIGGER lost_terminal`)
				if resume {
					if err := e.Run(t.Context(), id); err == nil || errors.Is(err, ErrReconciliation) {
						t.Fatal(err)
					}
				} else if err := e.Recover(t.Context(), id); err != nil && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				assertUninvoked()
				r, err := s.SkillRunWithHooks(t.Context(), "s", id)
				if err != nil || r.V2.Execution != "failed" {
					t.Fatal(r, err)
				}
				for i := 0; i < 8; i++ {
					if err = e.Recover(t.Context(), id); err != nil {
						t.Fatal(err)
					}
				}
				if err = e.Run(t.Context(), id); !errors.Is(err, ErrConflict) {
					t.Fatal("terminal Run replay", err)
				}
				assertUninvoked()
				after, _ := s.SkillHookReceipts(t.Context(), "s", id)
				now, _ := s.EngineCheckpoint(t.Context(), id)
				if len(after.Receipts) != len(before.Receipts)+1 || after.Receipts[len(after.Receipts)-1].Event != "run_terminal" || now.Steps != cp.Steps {
					t.Fatal(before, after, now)
				}
				var journals int
				s.db.QueryRow(`SELECT count(*) FROM side_effect_records WHERE workflow_id=?`, id).Scan(&journals)
				want := 0
				if last {
					want = 1
				}
				if journals != want {
					t.Fatal(journals)
				}
			})
		}
	}
}
func hookJournal(t *testing.T, s *Store, id string, result *ToolResult) (*Engine, Checkpoint) {
	t.Helper()
	e, r, cp := hookPending(t, s, id)
	if err := e.startSkillNode(t.Context(), r, "owned", &cp); err != nil {
		t.Fatal(err)
	}
	if result != nil {
		b := e.broker.(*SkillRunBroker)
		p, err := b.native.loadPrepared(t.Context(), cp.Pending[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		if err = b.native.skillDispatchClaim(t.Context(), p); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(result)
		if err = (runtime.SQLiteSideEffectStore{DB: s.db}).Complete(t.Context(), effectKey(id, p.Proposal.ID), string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	s.db.Exec(`UPDATE interaction_engine SET owner='' WHERE run_id=?`, id)
	return e, cp
}
func TestHooksObservedRecovery(t *testing.T) {
	for _, failed := range []bool{false, true} {
		for _, revoke := range []bool{false, true} {
			t.Run(fmt.Sprintf("failed=%v/revoke=%v", failed, revoke), func(t *testing.T) {
				s, id := admitHookRun(t, "")
				result := ToolResult{Content: "actual known result", IsError: failed}
				e, _ := hookJournal(t, s, id, &result)
				if revoke {
					hookEnable(t, s, hookPolicyExample)
				}
				if err := e.Recover(t.Context(), id); err != nil && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				r, err := s.SkillRunWithHooks(t.Context(), "s", id)
				if err != nil || r.V2.Nodes[0].Result == nil || *r.V2.Nodes[0].Result != result {
					t.Fatal(r, err)
				}
				receipts, _ := s.SkillHookReceipts(t.Context(), "s", id)
				if len(receipts.Receipts) < 2 || receipts.Receipts[1].Event != "after_node" || receipts.Receipts[1].Status != r.V2.Nodes[0].Outcome {
					t.Fatal(receipts)
				}
				if revoke && (r.V2.Execution != "revoked" || receipts.Receipts[len(receipts.Receipts)-1].Status != "revoked") {
					t.Fatal(r, receipts)
				}
				for i := 0; i < 3; i++ {
					if err = e.Recover(t.Context(), id); err != nil {
						t.Fatal(err)
					}
				}
				again, _ := s.SkillHookReceipts(t.Context(), "s", id)
				if operatorHash(receipts) != operatorHash(again) {
					t.Fatal("duplicate audit")
				}
			})
		}
	}
}
func TestHooksUnfinishedJournalStaysUnknown(t *testing.T) {
	s, id := admitHookRun(t, "")
	e, _ := hookJournal(t, s, id, nil)
	hookEnable(t, s, hookPolicyExample)
	for i := 0; i < 5; i++ {
		if err := e.Recover(t.Context(), id); !errors.Is(err, ErrReconciliation) {
			t.Fatal(err)
		}
	}
	r, err := s.SkillRunWithHooks(t.Context(), "s", id)
	receipts, _ := s.SkillHookReceipts(t.Context(), "s", id)
	if err != nil || r.V2.Nodes[0].Outcome != "unknown" || r.V2.Execution != "needs_reconciliation" || len(receipts.Receipts) != 1 {
		t.Fatal(r, receipts, err)
	}
}
func TestHooksTerminalReceiptOnce(t *testing.T) {
	s, id := admitHookRun(t, "")
	hookOwnedFile(t, s)
	e, _ := NewSkillRunEngine(s, id)
	if err := e.Run(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := e.Recover(t.Context(), id); err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	receipts, err := s.SkillHookReceipts(t.Context(), "s", id)
	if err != nil || len(receipts.Receipts) != 5 || receipts.Receipts[4].Status != "completed" {
		t.Fatal(receipts, err)
	}
}
func TestHooksAuditFailureTruth(t *testing.T) {
	s, id := admitHookRun(t, "")
	actual := ToolResult{Content: "known failed observation", IsError: true}
	e, _ := hookJournal(t, s, id, &actual)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_after BEFORE INSERT ON interaction_skill_hook_receipts WHEN NEW.event='after_node' BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if err := e.Recover(t.Context(), id); err == nil {
		t.Fatal("audit persistence fault ignored")
	}
	r, _ := s.SkillRunWithHooks(t.Context(), "s", id)
	if r.V2.Nodes[0].Result != nil {
		t.Fatal("partial publication")
	}
	s.db.Exec(`DROP TRIGGER fail_after`)
	if err := e.Recover(t.Context(), id); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	r, _ = s.SkillRunWithHooks(t.Context(), "s", id)
	if r.V2.Execution != "failed" || r.V2.Nodes[0].Result == nil || *r.V2.Nodes[0].Result != actual {
		t.Fatal(r)
	}
	// Pure evaluation construction failure is a fixed receipt, not raw errors or
	// authority. Validated immutable snapshots cannot normally enter this path.
	h := HookRunSnapshot{Policy: HookPolicy{SchemaVersion: 99}}
	rct := hookReceipt(h, "s", "r", HookMetadata{Event: "after_node", NodeID: "first", Capability: "workspace.read", Status: "failed"}, ToolProposal{})
	if rct.Decision != "evaluation_error" || rct.Reason != "hook_evaluation_failed" || rct.Status != "failed" || len(rct.Matches) != 0 {
		t.Fatal(rct)
	}
}

func TestHooksObservedRecoveryNativeClaimRace(t *testing.T) {
	for _, point := range []string{"before_native", "after_native"} {
		t.Run(point, func(t *testing.T) {
			s, id := admitHookRun(t, strings.Replace(hookPolicyExample, `"deny"`, `"audit"`, 1))
			hookOwnedFile(t, s)
			hookPrepare(t, s, "emergency-disable", "disable", "", "")
			trigger := `CREATE TRIGGER select_at_claim AFTER INSERT ON side_effect_records WHEN NEW.workflow_id='` + id + `' BEGIN UPDATE interaction_hook_sessions SET state='disabled',revision_id='',generation=generation+1 WHERE session_id='s'; END`
			if point == "after_native" {
				trigger = `CREATE TRIGGER select_at_claim AFTER UPDATE OF executed ON interaction_tool_proposals WHEN NEW.run_id='` + id + `' AND NEW.executed=1 BEGIN UPDATE interaction_hook_sessions SET state='disabled',revision_id='',generation=generation+1 WHERE session_id='s'; END`
			}
			if _, e := s.db.Exec(trigger); e != nil {
				t.Fatal(e)
			}
			engine, _ := NewSkillRunEngine(s, id)
			err := engine.Run(t.Context(), id)
			r, e := s.SkillRunWithHooks(t.Context(), "s", id)
			receipts, _ := s.SkillHookReceipts(t.Context(), "s", id)
			if e != nil {
				t.Fatal(e)
			}
			if point == "before_native" {
				var executed int
				var status string
				s.db.QueryRow(`SELECT sum(executed) FROM interaction_tool_proposals WHERE run_id=?`, id).Scan(&executed)
				s.db.QueryRow(`SELECT status FROM side_effect_records WHERE workflow_id=?`, id).Scan(&status)
				if !errors.Is(err, ErrReconciliation) || r.V2.Nodes[0].Outcome != "unknown" || r.V2.Nodes[0].Result != nil || executed != 0 || status != "started" || len(receipts.Receipts) != 1 {
					t.Fatal(err, r.V2.Nodes, executed, status, receipts)
				}
				for i := 0; i < 3; i++ {
					if e = engine.Recover(t.Context(), id); !errors.Is(e, ErrReconciliation) {
						t.Fatal(e)
					}
				}
			} else {
				if r.V2.Execution != "revoked" || r.V2.Nodes[0].Outcome != "success" || r.V2.Nodes[0].Result == nil || !strings.Contains(r.V2.Nodes[0].Result.Content, "install owned") || r.V2.Nodes[1].Outcome != "not_started" || len(receipts.Receipts) != 3 || receipts.Receipts[2].Status != "revoked" {
					t.Fatal(err, r, receipts)
				}
			}
		})
	}
}
func TestHooksObservedRecoveryFailedFinalWrite(t *testing.T) {
	for _, last := range []bool{false, true} {
		t.Run(fmt.Sprint(last), func(t *testing.T) {
			s, in := skillRunSetup(t)
			if last {
				in.Inputs = json.RawMessage(`{"first":{"path":"README.md"},"second":{"paths":["missing"],"query":"install"}}`)
				hookOwnedFile(t, s)
			}
			p, e := s.PrepareSkillRunWithHooks(t.Context(), "s", in)
			if e != nil {
				t.Fatal(e)
			}
			a, e := s.DecideSkillRunWithHooks(t.Context(), "s", OperatorDecision{RequestID: "confirm", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
			if e != nil {
				t.Fatal(e)
			}
			id := a.V2.RunID
			s.db.Exec(`CREATE TRIGGER fail_terminal BEFORE UPDATE OF state ON interaction_runs WHEN NEW.state='failed' BEGIN SELECT RAISE(ABORT,'fixture'); END`)
			engine, _ := NewSkillRunEngine(s, id)
			if e = engine.Run(t.Context(), id); e == nil || errors.Is(e, ErrReconciliation) {
				t.Fatal(e)
			}
			before, _ := s.SkillHookReceipts(t.Context(), "s", id)
			s.db.Exec(`DROP TRIGGER fail_terminal`)
			if e = engine.Recover(t.Context(), id); e != nil {
				t.Fatal(e)
			}
			r, e := s.SkillRunWithHooks(t.Context(), "s", id)
			after, _ := s.SkillHookReceipts(t.Context(), "s", id)
			index := 0
			if last {
				index = 1
			}
			if e != nil || r.V2.Execution != "failed" || r.V2.Nodes[index].Outcome != "failed" || len(after.Receipts) != len(before.Receipts)+1 || after.Receipts[len(after.Receipts)-1].Status != "failed" {
				t.Fatal(r, after, e)
			}
		})
	}
}
