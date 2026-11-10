package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewPersistedFailureDoesNotRecoverToSuccess(t *testing.T) {
	for _, first := range []bool{false, true} {
		for _, resume := range []bool{false, true} {
			t.Run(fmt.Sprintf("first=%v/resume=%v", first, resume), func(t *testing.T) {
				s, in := skillRunSetup(t)
				in.Inputs = json.RawMessage(`{"first":{"path":"README.md"},"second":{"paths":["missing.txt"],"query":"install"}}`)
				if first {
					in.Inputs = json.RawMessage(`{"first":{"path":"missing.txt"},"second":{"paths":["README.md"],"query":"install"}}`)
				}
				p, err := s.PrepareSkillRun(t.Context(), "s", in)
				if err != nil {
					t.Fatal(err)
				}
				a, err := s.DecideSkillRun(t.Context(), "s", OperatorDecision{RequestID: "confirm", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
				if err != nil {
					t.Fatal(err)
				}
				id := a.RunID
				w, _ := s.Workspace(t.Context(), "s")
				if err := os.WriteFile(filepath.Join(w.Policy.Root, "README.md"), []byte("install"), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(`CREATE TRIGGER fail_terminal BEFORE UPDATE OF state ON interaction_runs WHEN NEW.state='failed' BEGIN SELECT RAISE(ABORT,'simulate terminal write failure'); END`); err != nil {
					t.Fatal(err)
				}
				e, err := NewSkillRunEngine(s, id)
				if err != nil {
					t.Fatal(err)
				}
				if err = e.Run(t.Context(), id); err == nil {
					t.Fatal("expected terminal write failure")
				}
				before, err := s.SkillRun(t.Context(), "s", id)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("before recovery: execution=%s node0=%s node1=%s", before.Execution, before.Nodes[0].Outcome, before.Nodes[1].Outcome)
				if _, err := s.db.Exec(`DROP TRIGGER fail_terminal`); err != nil {
					t.Fatal(err)
				}
				if resume {
					if err := e.Run(t.Context(), id); !errors.Is(err, errSkillNodeFailed) {
						t.Fatal(err)
					}
				} else if err := e.Recover(t.Context(), id); err != nil {
					t.Fatal(err)
				}
				after, err := s.SkillRun(t.Context(), "s", id)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("after recovery: execution=%s node0=%s node1=%s", after.Execution, after.Nodes[0].Outcome, after.Nodes[1].Outcome)
				if after.Execution != "failed" {
					t.Fatalf("known failed last node recovered as %s", after.Execution)
				}
				if first && after.Nodes[1].Outcome != "not_started" {
					t.Fatal(after.Nodes)
				}
			})
		}
	}
}

func TestReviewSearchBudgetIsKnownFailure(t *testing.T) {
	s, _ := skillRunSetup(t)
	original, err := ValidateSkillManifest(readonlyManifest)
	if err != nil {
		t.Fatal(err)
	}
	m := original.Manifest
	m.ID = "review.output"
	m.Inputs = map[string]SkillField{}
	m.Workflow.Nodes = []SkillNode{}
	m.Budget = SkillBudget{MaxIterations: 5, MaxToolCalls: 5}
	inputs := map[string]any{}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		m.Inputs[id] = SkillField{Type: "object"}
		cap := "workspace.read"
		inputs[id] = map[string]any{"path": "README.md"}
		if id == "e" {
			cap = "workspace.search"
			inputs[id] = map[string]any{"paths": []string{"README.md"}, "query": "install"}
		}
		m.Workflow.Nodes = append(m.Workflow.Nodes, SkillNode{ID: id, Capability: cap, DependsOn: []string{}})
	}
	raw, _ := json.Marshal(m)
	imported := skillDecide(t, s, skillPrepare(t, s, "review-import", string(raw)), "review-import-d", "confirm")
	enable, err := s.PrepareSkill(t.Context(), "s", SkillIntent{RequestID: "review-enable", Kind: "enable", CatalogID: imported.CatalogID, RevisionID: imported.RevisionID})
	if err != nil {
		t.Fatal(err)
	}
	skillDecide(t, s, enable, "review-enable-d", "confirm")
	args, _ := json.Marshal(inputs)
	p, err := s.PrepareSkillRun(t.Context(), "s", SkillRunIntent{RequestID: "review-preview", CatalogID: imported.CatalogID, RevisionID: imported.RevisionID, ExecutionProfile: SkillRunProfile, Inputs: args})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.DecideSkillRun(t.Context(), "s", OperatorDecision{RequestID: "review-confirm", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
	if err != nil || a.State != "admitted" {
		t.Fatal(a, err)
	}
	id := a.RunID
	w, _ := s.Workspace(t.Context(), "s")
	data := []byte("install" + strings.Repeat("x", 220000))
	if err := os.WriteFile(filepath.Join(w.Policy.Root, "README.md"), data, 0600); err != nil {
		t.Fatal(err)
	}
	e, err := NewSkillRunEngine(s, id)
	if err != nil {
		t.Fatal(err)
	}
	err = e.Run(t.Context(), id)
	r, re := s.SkillRun(t.Context(), "s", id)
	if re != nil {
		t.Fatal(re)
	}
	t.Logf("run error=%v execution=%s nodes=%+v", err, r.Execution, r.Nodes)
	if errors.Is(err, ErrReconciliation) || r.Execution != "failed" || r.Nodes[4].Outcome != "failed" {
		t.Fatal("known search reservation refusal became unknown")
	}
}

func TestReviewNativeClaimRejectsTamperedAction(t *testing.T) {
	for _, mutation := range []string{"", "unknown", "file_undo", "pending", "budget", "missing"} {
		t.Run(mutation, func(t *testing.T) {
			s, id := admitTestSkillRun(t)
			w, _ := s.Workspace(t.Context(), "s")
			if err := os.WriteFile(filepath.Join(w.Policy.Root, "README.md"), []byte("private contents"), 0600); err != nil {
				t.Fatal(err)
			}
			b, err := NewSkillRunBroker(s, id)
			if err != nil {
				t.Fatal(err)
			}
			cp, err := s.EngineCheckpoint(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			p, err := b.Propose(t.Context(), id, cp.Calls[0])
			if err != nil {
				t.Fatal(err)
			}
			if mutation != "pending" && mutation != "budget" && mutation != "missing" {
				cp.Action = mutation
			}
			cp.Pending = []ToolProposal{p}
			cp.Calls = cp.Calls[1:]
			cp.Phase = PhaseExecuting
			cp.Steps = 1
			if mutation == "pending" {
				cp.Pending[0].Call.ID = "foreign"
			}
			if mutation == "budget" {
				cp.Budgets.MaxSteps++
			}
			raw, _ := json.Marshal(cp)
			if _, err := s.db.Exec(`UPDATE interaction_engine SET phase=?,checkpoint=? WHERE run_id=?`, cp.Phase, string(raw), id); err != nil {
				t.Fatal(err)
			}
			if mutation == "missing" {
				if _, err := s.db.Exec("DELETE FROM interaction_engine WHERE run_id=?", id); err != nil {
					t.Fatal(err)
				}
			}
			result, err := b.Execute(context.Background(), p.ID)
			var executed int
			if er := s.db.QueryRow(`SELECT executed FROM interaction_tool_proposals WHERE id=?`, p.ID).Scan(&executed); er != nil {
				t.Fatal(er)
			}
			t.Logf("error=%v executed=%d result=%+v", err, executed, result)
			if executed != 0 || err == nil && !result.IsError {
				t.Fatal("tampered action was accepted at durable native claim")
			}
		})
	}
}

func TestReviewTerminalCommitRejectsChangedCheckpointAction(t *testing.T) {
	for _, mutation := range []string{"", "unknown", "file_undo", "budget", "missing"} {
		t.Run(mutation, func(t *testing.T) {
			s, id := admitTestSkillRun(t)
			w, _ := s.Workspace(t.Context(), "s")
			if err := os.WriteFile(filepath.Join(w.Policy.Root, "README.md"), []byte("install"), 0600); err != nil {
				t.Fatal(err)
			}
			change := fmt.Sprintf("UPDATE interaction_engine SET checkpoint=json_set(checkpoint,'$.Action','%s') WHERE run_id=NEW.run_id;", mutation)
			if mutation == "budget" {
				change = "UPDATE interaction_engine SET checkpoint=json_set(checkpoint,'$.Budgets.MaxSteps',99) WHERE run_id=NEW.run_id;"
			}
			if mutation == "missing" {
				change = "DELETE FROM interaction_engine WHERE run_id=NEW.run_id;"
			}
			if _, err := s.db.Exec("CREATE TRIGGER tamper_checkpoint_after_observation AFTER UPDATE OF result ON interaction_skill_node_results WHEN NEW.node_id='second' BEGIN " + change + " END"); err != nil {
				t.Fatal(err)
			}

			e, err := NewSkillRunEngine(s, id)
			if err != nil {
				t.Fatal(err)
			}
			err = e.Run(t.Context(), id)
			r, re := s.SkillRun(t.Context(), "s", id)
			if re != nil {
				t.Fatal(re)
			}
			t.Logf("error=%v execution=%s", err, r.Execution)
			if !errors.Is(err, ErrReconciliation) || r.Execution == "completed" {
				t.Fatal("terminal transaction overwrote inconsistent persisted action and claimed success")
			}
		})
	}
}
