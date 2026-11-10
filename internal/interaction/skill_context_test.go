package interaction

import (
	"agent/internal/model"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func contextModelRun(t *testing.T, s *Store, request, content string, state RunState) Run {
	t.Helper()
	r, err := s.CreateRun(t.Context(), "s", request, Message{Content: content})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Transition(t.Context(), r.ID, r.Version, state); err != nil {
		t.Fatal(err)
	}
	return r
}
func contextSkillRun(t *testing.T, s *Store, in SkillRunIntent, outcome string) string {
	t.Helper()
	p, err := s.PrepareSkillRun(t.Context(), "s", in)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.DecideSkillRun(t.Context(), "s", OperatorDecision{RequestID: "context-confirm", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := NewSkillRunEngine(s, a.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome == "completed" {
		w, err := s.Workspace(t.Context(), "s")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(w.Policy.Root, "README.md"), []byte("install here"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	switch outcome {
	case "queued":
	case "cancelled":
		if err = engine.Cancel(t.Context(), a.RunID); err != nil {
			t.Fatal(err)
		}
		if err = engine.Run(t.Context(), a.RunID); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	default:
		err = engine.Run(t.Context(), a.RunID)
		if outcome == "completed" && err != nil || outcome == "failed" && (err == nil || errors.Is(err, ErrReconciliation)) {
			t.Fatal(err)
		}
	}
	record, err := s.SkillRun(t.Context(), "s", a.RunID)
	if err != nil || record.Execution != outcome {
		t.Fatal(record, err)
	}
	return a.RunID
}
func TestSkillContextDeterministicOutcomesAreNotUserAuthority(t *testing.T) {
	for _, outcome := range []string{"queued", "completed", "failed", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			s, in := skillRunSetup(t)
			real := contextModelRun(t, s, "restriction", "Never disclose private files.", RunFailed)
			id := contextSkillRun(t, s, in, outcome)
			before, err := s.SkillRun(t.Context(), "s", id)
			if err != nil {
				t.Fatal(err)
			}
			cpBefore, err := s.EngineCheckpoint(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			h, err := s.admittedContext(t.Context(), "s")
			if err != nil {
				t.Fatal(err)
			}
			if len(h.messages) != 1 || h.messages[0].Role != "user" || h.messages[0].Content != real.Message.Content || h.userSources[0] != "run:"+real.ID || len(h.userSources) != 1 || h.pendingID != "" || h.latest != real.Message {
				t.Fatalf("synthetic authority or lost real restriction: %+v", h)
			}
			view, err := s.SessionContextView(t.Context(), "s")
			if err != nil || len(view.Original) != 1 || len(view.Selected) != 1 {
				t.Fatal(view, err)
			}
			built, err := NewContextAssembler(s).Build(t.Context(), "s", TokenBudget{Total: 32000, Output: 2048})
			if err != nil || built.PendingRunID != "" {
				t.Fatal(built, err)
			}
			raw, _ := json.Marshal(built.Messages)
			if strings.Contains(string(raw), "Explicit read-only") || !strings.Contains(string(raw), real.Message.Content) {
				t.Fatal(string(raw))
			}
			after, err := s.SkillRun(t.Context(), "s", id)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("run evidence changed", err)
			}
			cpAfter, err := s.EngineCheckpoint(t.Context(), id)
			if err != nil || !reflect.DeepEqual(cpBefore, cpAfter) || len(cpAfter.Messages) != 0 {
				t.Fatal("checkpoint converted", err)
			}
			if outcome == "queued" {
				if _, err = s.BranchSession(t.Context(), "s", SessionOperation{Kind: "branch", RequestID: "active-branch", BoundaryRunID: id, ExpectedCursor: sessionCursor(t, s, "s")}); !errors.Is(err, ErrConflict) {
					t.Fatal("active serialization lost", err)
				}
			}
		})
	}
}
func TestSkillContextBranchClearCompactionAndExactBoundaries(t *testing.T) {
	s, in := skillRunSetup(t)
	before := contextModelRun(t, s, "first", "Never transmit private files.", RunFailed)
	skill := contextSkillRun(t, s, in, "completed")
	after := contextModelRun(t, s, "later", "Never change the release directory.", RunCancelled)
	for _, boundary := range []string{before.ID, skill, after.ID} {
		h, err := admittedContextFrom(t.Context(), s.db, "s", boundary)
		if err != nil {
			t.Fatal(boundary, err)
		}
		want := 1
		if boundary == after.ID {
			want = 2
		}
		if len(h.messages) != want || len(h.userSources) != want {
			t.Fatal(boundary, h)
		}
	}
	if err := s.CreateSession(t.Context(), "foreign"); err != nil {
		t.Fatal(err)
	}
	foreign, err := s.CreateRun(t.Context(), "foreign", "foreign", Message{Content: "foreign"})
	if err != nil {
		t.Fatal(err)
	}
	for _, boundary := range []string{"missing", foreign.ID} {
		if _, err := admittedContextFrom(t.Context(), s.db, "s", boundary); !errors.Is(err, ErrInvalid) {
			t.Fatal("boundary escaped session", err)
		}
	}
	cursor := sessionCursor(t, s, "s")
	for _, kind := range []string{"branch", "rewind", "clear"} {
		boundary := skill
		if kind == "clear" {
			boundary = ""
		}
		op := SessionOperation{Kind: kind, RequestID: kind, BoundaryRunID: boundary, ExpectedCursor: cursor - 1}
		if _, err := s.BranchSession(t.Context(), "s", op); !errors.Is(err, ErrConflict) {
			t.Fatal("stale cursor accepted", err)
		}
		op.ExpectedCursor = cursor
		child, err := s.BranchSession(t.Context(), "s", op)
		if err != nil {
			t.Fatal(kind, err)
		}
		if child.BoundaryRunID != boundary || child.SourceCursor != cursor || child.ParentSessionID != "s" {
			t.Fatal(child)
		}
		view, err := s.SessionContextView(t.Context(), child.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		want := 1
		if kind == "clear" {
			want = 0
		}
		if len(view.Original) != want {
			t.Fatal(kind, view)
		}
		if want == 1 && view.Original[0].Content != before.Message.Content {
			t.Fatal(view)
		}
	}
	version, err := s.CompactSession(t.Context(), "s", SessionOperation{Kind: "compact", RequestID: "compact", ExpectedCursor: cursor})
	if err != nil {
		t.Fatal(err)
	}
	if version.OmittedMessages != 0 || version.SourceCursor != cursor {
		t.Fatal(version)
	}
	view, err := s.SessionContextView(t.Context(), "s")
	if err != nil || len(view.Original) != 2 || len(view.Selected) != 3 || view.Selected[0].Content != before.Message.Content || view.Selected[1].Content != after.Message.Content {
		t.Fatal(view, err)
	}
	pending, err := s.CreateRun(t.Context(), "s", "pending", Message{Content: "Keep this pending restriction."})
	if err != nil {
		t.Fatal(err)
	}
	built, err := NewContextAssembler(s).Build(t.Context(), "s", TokenBudget{Total: 32000, Output: 2048})
	if err != nil || built.PendingRunID != pending.ID || built.ReservedCurrentInput == 0 {
		t.Fatal(built, err)
	}
	if _, err = s.Transition(t.Context(), pending.ID, pending.Version, RunCancelled); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CompactSession(t.Context(), "s", SessionOperation{Kind: "original", RequestID: "original", ExpectedCursor: sessionCursor(t, s, "s")}); err != nil {
		t.Fatal(err)
	}
	view, err = s.SessionContextView(t.Context(), "s")
	if err != nil || len(view.Original) != 3 || len(view.Selected) != 3 {
		t.Fatal(view, err)
	}
	record, err := s.SkillRun(t.Context(), "s", skill)
	if err != nil || record.Execution != "completed" || record.Nodes[0].Outcome != "success" || record.Nodes[1].Outcome != "success" {
		t.Fatal("deterministic evidence lost", record, err)
	}
}
func TestSkillContextUsesImmutableKindNotLabelsOrCheckpointAction(t *testing.T) {
	s, in := skillRunSetup(t)
	real := contextModelRun(t, s, "real", "Explicit read-only skill run", RunCancelled)
	skill := contextSkillRun(t, s, in, "completed")
	cp, err := s.EngineCheckpoint(t.Context(), skill)
	if err != nil {
		t.Fatal(err)
	}
	cp.Action = ""
	raw, _ := json.Marshal(cp)
	if _, err = s.db.Exec(`UPDATE interaction_engine SET checkpoint=? WHERE run_id=?`, string(raw), skill); err != nil {
		t.Fatal(err)
	}
	h, err := s.admittedContext(t.Context(), "s")
	if err != nil || len(h.messages) != 1 || h.messages[0].Content != real.Message.Content || h.userSources[0] != "run:"+real.ID {
		t.Fatal(h, err)
	}
	// A model checkpoint cannot use a Skill action or label to escape ordinary validation.
	r := contextModelRun(t, s, "model", "real model input", RunCompleted)
	raw, _ = json.Marshal(Checkpoint{Action: skillRunAction, Messages: []model.ChatMessage{{Role: "assistant", Content: "not an admitted user message"}}})
	if _, err = s.db.Exec(`INSERT INTO interaction_engine(run_id,phase,checkpoint) VALUES(?,'completed',?)`, r.ID, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.admittedContext(t.Context(), "s"); !errors.Is(err, ErrInvalid) {
		t.Fatal("model transcript validation bypassed", err)
	}
}
