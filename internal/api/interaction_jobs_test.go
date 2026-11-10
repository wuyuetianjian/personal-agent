package api

import (
	"agent/internal/interaction"
	"agent/internal/model"
	"agent/internal/runtime"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJobsBusyOriginAndNoContextInjection(t *testing.T) {
	entered := make(chan string, 8)
	release := make(chan struct{})
	defer close(release)
	p := testSelection()
	p.Provider = interactionStreamFunc(func(ctx context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
		text := r.Messages[len(r.Messages)-1].Content
		entered <- text
		if text == "parent" {
			select {
			case <-release:
			case <-ctx.Done():
				return model.TurnResult{}, ctx.Err()
			}
		}
		return model.TurnResult{Content: "job result"}, nil
	})
	s, _ := serviceFixture(t, p)
	id := createChat(t, s)
	path := "/api/v1/sessions/" + id
	if w := apiRequest(s, "POST", path+"/messages", `{"request_id":"parent","content":"parent"}`); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	<-entered
	w := apiRequest(s, "POST", path+"/jobs", `{"request_id":"job","content":"independent"}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var op interaction.JobOperationRecord
	json.Unmarshal(w.Body.Bytes(), &op)
	select {
	case text := <-entered:
		if text != "independent" {
			t.Fatal(text)
		}
	case <-time.After(time.Second * 5):
		t.Fatal("not started")
	}
	awaitState(t, s, op.Job.SessionID, "completed")
	snap, _ := s.snapshot(t.Context(), id)
	if len(snap.Runs) != 1 || snap.Runs[0].Input != "parent" || snap.Runs[0].State != "running" {
		t.Fatal(snap)
	}
	w = apiRequest(s, "POST", path+"/jobs", `{"request_id":"job","content":"independent"}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = apiRequest(s, "POST", "/api/v1/sessions/"+op.Job.SessionID+"/messages", `{"request_id":"bypass","content":"new"}`); w.Code != 409 {
		t.Fatal("mutable job context", w.Code)
	}
}
func TestJobsNativeApprovalReadOnlyAndOriginAttribution(t *testing.T) {
	for _, decision := range []string{"readonly", "denied", "approved"} {
		t.Run(decision, func(t *testing.T) {
			p := testSelection()
			p.ToolsSupported = true
			var scope string
			p.Provider = interactionStreamFunc(func(ctx context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
				if r.Messages[len(r.Messages)-1].Role == "tool" {
					return model.TurnResult{Content: "done"}, nil
				}
				args, _ := json.Marshal(interaction.PatchArguments{Scope: scope, Path: "note.txt", Preimage: fmt.Sprintf("%x", sha256.Sum256([]byte("old"))), Content: "new"})
				return model.TurnResult{ToolCalls: []model.ToolCall{{ID: "patch", Name: "apply_patch", Arguments: string(args)}}}, nil
			})
			s, _ := serviceFixture(t, p)
			os.WriteFile(filepath.Join(s.options.Workspace, "note.txt"), []byte("old"), 0600)
			w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"create","mode":"acceptEdits"}`)
			var session SessionView
			json.Unmarshal(w.Body.Bytes(), &session)
			bound, _ := s.store.Workspace(t.Context(), session.ID)
			scope = bound.ScopeID
			path := "/api/v1/sessions/" + session.ID + "/jobs"
			w = apiRequest(s, "POST", path, fmt.Sprintf(`{"request_id":"job","content":"edit","effects":%t}`, decision != "readonly"))
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			var op interaction.JobOperationRecord
			json.Unmarshal(w.Body.Bytes(), &op)
			if decision == "readonly" {
				awaitState(t, s, op.Job.SessionID, "failed")
			} else {
				snap := awaitState(t, s, op.Job.SessionID, "waiting_approval")
				a := snap.Runs[0].Approval
				if a == nil {
					t.Fatal(snap)
				}
				w = apiRequest(s, "GET", "/api/v1/approvals/"+a.ID+"/review", "")
				if w.Code != 200 {
					t.Fatal(w.Body.String())
				}
				w = apiRequest(s, "POST", path+"/"+op.Job.ID+"/decision", fmt.Sprintf(`{"request_id":"decision","approval_id":%q,"digest":%q,"decision":%q}`, a.ID, a.Digest, decision))
				if w.Code != 202 {
					t.Fatal(w.Code, w.Body.String())
				}
				terminal := "completed"
				if decision == "denied" {
					terminal = "failed"
				}
				awaitState(t, s, op.Job.SessionID, terminal)
				w = apiRequest(s, "POST", path+"/"+op.Job.ID+"/decision", fmt.Sprintf(`{"request_id":"decision","approval_id":%q,"digest":%q,"decision":%q}`, a.ID, a.Digest, decision))
				if w.Code != 202 {
					t.Fatal("repeat decision", w.Body.String())
				}
			}
			data, _ := os.ReadFile(filepath.Join(s.options.Workspace, "note.txt"))
			want := "old"
			if decision == "approved" {
				want = "new"
			}
			if string(data) != want {
				t.Fatal(string(data), want)
			}
			snap, _ := s.snapshot(t.Context(), session.ID)
			if len(snap.Runs) != 0 {
				t.Fatal("origin changed", snap)
			}
		})
	}
}
func TestJobsStopIndependentAndAdmissionCrashReopen(t *testing.T) {
	p := testSelection()
	p.Provider = interactionStreamFunc(func(ctx context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
		<-ctx.Done()
		return model.TurnResult{}, ctx.Err()
	})
	s, rt := serviceFixture(t, p)
	id := createChat(t, s)
	path := "/api/v1/sessions/" + id
	apiRequest(s, "POST", path+"/messages", `{"request_id":"p","content":"parent"}`)
	awaitState(t, s, id, "model")
	w := apiRequest(s, "POST", path+"/jobs", `{"request_id":"j","content":"job"}`)
	var op interaction.JobOperationRecord
	json.Unmarshal(w.Body.Bytes(), &op)
	awaitState(t, s, op.Job.SessionID, "model")
	w = apiRequest(s, "POST", path+"/jobs/"+op.Job.ID+"/stop", `{"request_id":"stop"}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	awaitState(t, s, op.Job.SessionID, "cancelled")
	snap, _ := s.snapshot(t.Context(), id)
	if snap.Runs[0].State != "running" {
		t.Fatal("job cancelled origin")
	}
	s.Close()
	j, e := s.store.CreateJob(t.Context(), id, "crash", "must not replay", "", false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.store.AdmitJob(t.Context(), j.ID); e != nil {
		t.Fatal(e)
	}
	next, e := NewInteractionService(rt, s.options)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	w = apiRequest(next, "POST", path+"/jobs", `{"request_id":"crash","content":"must not replay"}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var replay interaction.JobOperationRecord
	json.Unmarshal(w.Body.Bytes(), &replay)
	if replay.Job.ID != j.ID || replay.Job.RunID != j.RunID || replay.Job.State != "unknown" {
		t.Fatal(replay)
	}
	if w = apiRequest(next, "POST", "/api/v1/runs/"+j.RunID+"/resume", `{"request_id":"resume"}`); w.Code != 409 {
		t.Fatal("missing checkpoint resume", w.Body.String())
	}
}
func TestJobsLimitsAndNormalControlCapacity(t *testing.T) {
	p := testSelection()
	p.Provider = interactionStreamFunc(func(ctx context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
		<-ctx.Done()
		return model.TurnResult{}, ctx.Err()
	})
	s, _ := serviceFixture(t, p)
	id := createChat(t, s)
	path := "/api/v1/sessions/" + id
	for i := 0; i < interaction.JobOriginLimit; i++ {
		w := apiRequest(s, "POST", path+"/jobs", fmt.Sprintf(`{"request_id":"j%d","content":"wait"}`, i))
		if w.Code != 202 {
			t.Fatal(i, w.Body.String())
		}
	}
	w := apiRequest(s, "POST", path+"/jobs", `{"request_id":"overflow","content":"wait"}`)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	time.Sleep(time.Millisecond * 30)
	s.mu.Lock()
	n := len(s.workers)
	s.mu.Unlock()
	if n != 2 {
		t.Fatal("jobs executors", n)
	}
	w = apiRequest(s, "POST", path+"/messages", `{"request_id":"ordinary","content":"parent"}`)
	if w.Code != 202 {
		t.Fatal("jobs starved normal", w.Body.String())
	}
	snap := awaitState(t, s, id, "model")
	w = apiRequest(s, "POST", "/api/v1/runs/"+snap.Runs[0].ID+"/cancel", `{"request_id":"normalcancel"}`)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	awaitState(t, s, id, "cancelled")
	var overflow int
	s.runtime.DB.SQL.QueryRow("SELECT count(*) FROM interaction_jobs WHERE request_id='overflow'").Scan(&overflow)
	if overflow != 0 {
		t.Fatal("overflow persisted")
	}
}
func TestJobsPolicyRevocationAndNeverStartedRecovery(t *testing.T) {
	for _, kind := range []string{"provider", "root", "policy"} {
		t.Run(kind, func(t *testing.T) {
			s, rt := serviceFixture(t, testSelection())
			w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"create","mode":"default"}`)
			var origin SessionView
			json.Unmarshal(w.Body.Bytes(), &origin)
			s.Close()
			j, e := s.store.CreateJob(t.Context(), origin.ID, "job", "work", "", false)
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "provider":
				p := testSelection()
				p.Endpoint = "http://127.0.0.1:8123/changed"
				s.options.Resolve = func(context.Context, string) (ProviderSelection, error) { return p, nil }
			case "root":
				old := s.options.Workspace + "-old"
				if e = os.Rename(s.options.Workspace, old); e != nil {
					t.Fatal(e)
				}
				defer os.RemoveAll(old)
				os.Mkdir(s.options.Workspace, 0700)
			case "policy":
				rt.DB.SQL.Exec("DROP TRIGGER interaction_api_binding_immutable")
				if _, e = rt.DB.SQL.Exec("UPDATE interaction_api_sessions SET mode='plan' WHERE session_id=?", origin.ID); e != nil {
					t.Fatal(e)
				}
			}
			next, e := NewInteractionService(rt, s.options)
			if e != nil {
				t.Fatal(e)
			}
			defer next.Close()
			got, e := next.store.Job(t.Context(), j.ID)
			if e != nil || got.BlockedReason == "" || got.State != "registered" {
				t.Fatal(got, e)
			}
			var n int
			rt.DB.SQL.QueryRow("SELECT count(*) FROM interaction_runs WHERE id=?", j.RunID).Scan(&n)
			if n != 0 {
				t.Fatal("revoked job ran")
			}
		})
	}
	s, rt := serviceFixture(t, testSelection())
	id := createChat(t, s)
	s.Close()
	j, e := s.store.CreateJob(t.Context(), id, "job", "never admitted", "", false)
	if e != nil {
		t.Fatal(e)
	}
	next, e := NewInteractionService(rt, s.options)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	awaitState(t, next, j.SessionID, "completed")
	got, _ := next.store.Job(t.Context(), j.ID)
	if got.RunID != j.RunID {
		t.Fatal("new execution identity")
	}
}
func TestJobsUnknownStopDoesNotInventCheckpoint(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	id := createChat(t, s)
	j, e := s.store.CreateJob(t.Context(), id, "job", "unresolved", "", false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.store.AdmitJob(t.Context(), j.ID); e != nil {
		t.Fatal(e)
	}
	w := apiRequest(s, "POST", "/api/v1/sessions/"+id+"/jobs/"+j.ID+"/stop", `{"request_id":"stop"}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, e = s.store.EngineCheckpoint(t.Context(), j.RunID); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("manufactured checkpoint", e)
	}
	j, _ = s.store.Job(t.Context(), j.ID)
	if j.State != "unknown" {
		t.Fatal(j)
	}
}
func TestJobsRecoveredPendingApprovalStillExactAndNoAutoExecution(t *testing.T) {
	selection := testSelection()
	selection.ToolsSupported = true
	s, rt := serviceFixture(t, selection)
	w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"create","mode":"default"}`)
	var origin SessionView
	json.Unmarshal(w.Body.Bytes(), &origin)
	s.Close()
	// Reconstruct a genuinely checkpointed waiting run without a live owner.
	j, e := s.store.CreateJob(t.Context(), origin.ID, "job", "edit", "", true)
	if e != nil {
		t.Fatal(e)
	}
	run, e := s.store.AdmitJob(t.Context(), j.ID)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(s.options.Workspace, "note.txt"), []byte("old"), 0600)
	bound, _ := s.store.Workspace(t.Context(), j.SessionID)
	args, _ := json.Marshal(interaction.PatchArguments{Scope: bound.ScopeID, Path: "note.txt", Preimage: fmt.Sprintf("%x", sha256.Sum256([]byte("old"))), Content: "new"})
	p := testSelection()
	p.ToolsSupported = true
	p.Provider = interactionStreamFunc(func(_ context.Context, r model.TurnRequest, _ func(model.ModelEvent) error) (model.TurnResult, error) {
		if len(r.Messages) > 0 && r.Messages[len(r.Messages)-1].Role == "tool" {
			return model.TurnResult{Content: "done"}, nil
		}
		return model.TurnResult{ToolCalls: []model.ToolCall{{ID: "edit", Name: "apply_patch", Arguments: string(args)}}}, nil
	})
	engine, _ := interaction.NewEngine(s.store, p.Provider, s.broker, interaction.EngineOptions{Request: model.TurnRequest{ChatRequest: model.ChatRequest{Model: p.safeModel()}}})
	if e = engine.Run(t.Context(), run.ID); !errors.Is(e, interaction.ErrWaitingApproval) {
		t.Fatal(e)
	}
	// Preserve the exact provider binding; only its deterministic fixture callback changes.
	opts := s.options
	opts.Resolve = func(context.Context, string) (ProviderSelection, error) {
		x := selection
		x.Provider = p.Provider
		return x, nil
	}
	next, e := NewInteractionService(rt, opts)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	got, _ := next.store.Job(t.Context(), j.ID)
	if got.State != "waiting_approval" {
		t.Fatal("lost pending approval after offline recovery", got)
	}
	data, _ := os.ReadFile(filepath.Join(s.options.Workspace, "note.txt"))
	if string(data) != "old" {
		t.Fatal("restart executed edit")
	}
	snap, _ := next.snapshot(t.Context(), j.SessionID)
	a := snap.Runs[0].Approval
	if a == nil {
		t.Fatal("missing exact approval")
	}
	w = apiRequest(next, "POST", "/api/v1/sessions/"+origin.ID+"/jobs/"+j.ID+"/decision", fmt.Sprintf(`{"request_id":"approve-recovered","approval_id":%q,"digest":%q,"decision":"approved"}`, a.ID, a.Digest))
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	awaitState(t, next, j.SessionID, "completed")
	data, _ = os.ReadFile(filepath.Join(s.options.Workspace, "note.txt"))
	if string(data) != "new" {
		t.Fatal("recovered approval did not execute exact native edit")
	}
}

func TestJobsApprovedCrashAndUnknownEffectNeverReplay(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(fmt.Sprint(uncertain), func(t *testing.T) {
			selection := testSelection()
			selection.ToolsSupported = true
			s, rt := serviceFixture(t, selection)
			w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"create","mode":"default"}`)
			var origin SessionView
			json.Unmarshal(w.Body.Bytes(), &origin)
			s.Close()
			j, e := s.store.CreateJob(t.Context(), origin.ID, "job", "edit", "", true)
			if e != nil {
				t.Fatal(e)
			}
			run, e := s.store.AdmitJob(t.Context(), j.ID)
			if e != nil {
				t.Fatal(e)
			}
			os.WriteFile(filepath.Join(s.options.Workspace, "note.txt"), []byte("old"), 0600)
			bound, _ := s.store.Workspace(t.Context(), j.SessionID)
			args, _ := json.Marshal(interaction.PatchArguments{Scope: bound.ScopeID, Path: "note.txt", Preimage: fmt.Sprintf("%x", sha256.Sum256([]byte("old"))), Content: "new"})
			calls := 0
			selection.Provider = interactionStreamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
				calls++
				return model.TurnResult{ToolCalls: []model.ToolCall{{ID: "edit", Name: "apply_patch", Arguments: string(args)}}}, nil
			})
			engine, _ := interaction.NewEngine(s.store, selection.Provider, s.broker, interaction.EngineOptions{Request: model.TurnRequest{ChatRequest: model.ChatRequest{Model: selection.safeModel()}}})
			if e = engine.Run(t.Context(), run.ID); !errors.Is(e, interaction.ErrWaitingApproval) {
				t.Fatal(e)
			}
			cp, _ := s.store.EngineCheckpoint(t.Context(), run.ID)
			a := cp.Pending[0]
			fresh, e := s.store.DecideJob(t.Context(), origin.ID, j.ID, "decision", a.ID, a.Digest, "approved")
			if e != nil || !fresh {
				t.Fatal(e)
			}
			if uncertain {
				if e = s.store.ConsumeApproval(t.Context(), a.ID, a.Digest); e != nil {
					t.Fatal(e)
				}
				journal := runtime.SQLiteSideEffectStore{DB: rt.DB.SQL}
				if _, claimed, e := journal.Claim(t.Context(), runtime.SideEffectRecord{IdempotencyKey: fmt.Sprintf("interaction:%d:%s:%s", len(run.ID), run.ID, a.ID), WorkflowID: run.ID, NodeID: a.ID, CapabilityID: "apply_patch"}); e != nil || !claimed {
					t.Fatal(e)
				}
			}
			opts := s.options
			opts.Resolve = func(context.Context, string) (ProviderSelection, error) { return selection, nil }
			next, e := NewInteractionService(rt, opts)
			if e != nil {
				t.Fatal(e)
			}
			defer next.Close()
			got, _ := next.store.Job(t.Context(), j.ID)
			if got.State != "unknown" {
				t.Fatal(got)
			}
			if uncertain && got.Phase != "needs_reconciliation" {
				t.Fatal(got)
			}
			path := "/api/v1/sessions/" + origin.ID + "/jobs/" + j.ID
			w = apiRequest(next, "POST", path+"/decision", fmt.Sprintf(`{"request_id":"decision","approval_id":%q,"digest":%q,"decision":"approved"}`, a.ID, a.Digest))
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			if calls != 1 {
				t.Fatal("replayed provider", calls)
			}
			data, _ := os.ReadFile(filepath.Join(s.options.Workspace, "note.txt"))
			if string(data) != "old" {
				t.Fatal("replayed effect")
			}
			if uncertain {
				w = apiRequest(next, "POST", path+"/stop", `{"request_id":"stop"}`)
				if w.Code != 202 {
					t.Fatal(w.Body.String())
				}
				got, _ = next.store.Job(t.Context(), j.ID)
				if got.State != "unknown" {
					t.Fatal("uncertain cleanup reported terminal", got)
				}
			} else {
				w = apiRequest(next, "POST", path+"/stop", `{"request_id":"stop"}`)
				if w.Code != 202 {
					t.Fatal(w.Code, w.Body.String())
				}
				awaitState(t, next, j.SessionID, "cancelled")
				if calls != 1 {
					t.Fatal("stop replayed provider")
				}
			}
		})
	}
}
func TestJobsAdapterAndBudgetBoundaries(t *testing.T) {
	for _, tool := range []string{"exec", "mcp_untrusted", "read"} {
		t.Run(tool, func(t *testing.T) {
			p := testSelection()
			p.ToolsSupported = true
			p.Provider = interactionStreamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
				return model.TurnResult{ToolCalls: []model.ToolCall{{ID: "unsupported", Name: tool, Arguments: `{}`}}}, nil
			})
			s, _ := serviceFixture(t, p)
			w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"create","mode":"default"}`)
			var origin SessionView
			json.Unmarshal(w.Body.Bytes(), &origin)
			w = apiRequest(s, "POST", "/api/v1/sessions/"+origin.ID+"/jobs", `{"request_id":"job","content":"attempt unsupported"}`)
			if w.Code != 202 {
				t.Fatal(w.Body.String())
			}
			var op interaction.JobOperationRecord
			json.Unmarshal(w.Body.Bytes(), &op)
			awaitState(t, s, op.Job.SessionID, "failed")
			cp, e := s.store.EngineCheckpoint(t.Context(), op.Job.RunID)
			if e != nil || cp.Budgets.MaxSteps != 16 || cp.Budgets.MaxDuration != 2*time.Minute || cp.Budgets.MaxOutputBytes != 128<<10 || cp.Budgets.MaxToolResultBytes != 32<<10 || cp.Budgets.MaxEvents != 512 {
				t.Fatal(cp.Budgets, e)
			}
			bound, _ := s.store.Workspace(t.Context(), op.Job.SessionID)
			if bound.Policy.NonSandboxExec || len(bound.Policy.MCP) != 0 || bound.Policy.Mode != interaction.ModePlan {
				t.Fatal("inherited executable authority", bound)
			}
			var n int
			s.runtime.DB.SQL.QueryRow("SELECT count(*) FROM side_effect_records WHERE workflow_id=?", op.Job.RunID).Scan(&n)
			if n != 0 {
				t.Fatal("unsupported executed")
			}
		})
	}
}
func TestJobsModelOutputBudgetIsEnforced(t *testing.T) {
	p := testSelection()
	p.Provider = interactionStreamFunc(func(_ context.Context, _ model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
		text := strings.Repeat("x", (128<<10)+1)
		if e := emit(model.ModelEvent{Kind: model.EventTextDelta, Text: text}); e != nil {
			return model.TurnResult{}, e
		}
		return model.TurnResult{Content: text}, nil
	})
	s, _ := serviceFixture(t, p)
	id := createChat(t, s)
	w := apiRequest(s, "POST", "/api/v1/sessions/"+id+"/jobs", `{"request_id":"job","content":"large output"}`)
	var op interaction.JobOperationRecord
	json.Unmarshal(w.Body.Bytes(), &op)
	snap := awaitState(t, s, op.Job.SessionID, "failed")
	if len(snap.Runs[0].Text)+len(snap.Runs[0].Draft) > 128<<10 {
		t.Fatal("unbounded output persisted")
	}
	parent, _ := s.snapshot(t.Context(), id)
	if len(parent.Runs) != 0 {
		t.Fatal("budget failure altered origin")
	}
}
func TestJobsPlanCannotRequestEffectsAndOriginMustBelong(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"create","mode":"plan"}`)
	var origin SessionView
	json.Unmarshal(w.Body.Bytes(), &origin)
	path := "/api/v1/sessions/" + origin.ID + "/jobs"
	w = apiRequest(s, "POST", path, `{"request_id":"job","content":"edit","effects":true}`)
	if w.Code != 409 {
		t.Fatal("plan effects", w.Code, w.Body.String())
	}
	if _, e := s.store.JobOperation(t.Context(), origin.ID, "job"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("refusal recorded as created", e)
	}
	other := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"other","mode":"chat_only"}`)
	var otherSession SessionView
	json.Unmarshal(other.Body.Bytes(), &otherSession)
	r, e := s.store.CreateRun(t.Context(), otherSession.ID, "other-run", interaction.Message{Content: "foreign"})
	if e != nil {
		t.Fatal(e)
	}
	w = apiRequest(s, "POST", path, fmt.Sprintf(`{"request_id":"foreign","content":"work","origin_run_id":%q}`, r.ID))
	if w.Code != 409 {
		t.Fatal("unverified origin", w.Code, w.Body.String())
	}
}
func TestJobsStopDoesNotHideUnreconciledExecutionCheckpoint(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	id := createChat(t, s)
	j, e := s.store.CreateJob(t.Context(), id, "job", "interrupted", "", false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.store.AdmitJob(t.Context(), j.ID); e != nil {
		t.Fatal(e)
	}
	cp := interaction.Checkpoint{Phase: interaction.PhaseExecuting, Budgets: interaction.JobBudgets(), Deadline: time.Now().Add(time.Minute)}
	raw, _ := json.Marshal(cp)
	if _, e = s.runtime.DB.SQL.Exec("INSERT INTO interaction_engine(run_id,phase,checkpoint) VALUES(?,'executing',?)", j.RunID, string(raw)); e != nil {
		t.Fatal(e)
	}
	w := apiRequest(s, "POST", "/api/v1/sessions/"+id+"/jobs/"+j.ID+"/stop", `{"request_id":"stop"}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	time.Sleep(20 * time.Millisecond)
	got, _ := s.store.Job(t.Context(), j.ID)
	if got.State != "unknown" {
		t.Fatal("unreconciled effect hidden by cancellation phase", got)
	}
}
func TestJobsApprovalCannotConsumeReservedNormalSlots(t *testing.T) {
	p := testSelection()
	p.ToolsSupported = true
	var scope string
	p.Provider = interactionStreamFunc(func(ctx context.Context, r model.TurnRequest, _ func(model.ModelEvent) error) (model.TurnResult, error) {
		last := r.Messages[len(r.Messages)-1]
		if last.Content == "hold" {
			<-ctx.Done()
			return model.TurnResult{}, ctx.Err()
		}
		if last.Role == "tool" {
			return model.TurnResult{Content: "done"}, nil
		}
		args, _ := json.Marshal(interaction.PatchArguments{Scope: scope, Path: "note.txt", Preimage: fmt.Sprintf("%x", sha256.Sum256([]byte("old"))), Content: "new"})
		return model.TurnResult{ToolCalls: []model.ToolCall{{ID: "edit", Name: "apply_patch", Arguments: string(args)}}}, nil
	})
	s, _ := serviceFixture(t, p)
	os.WriteFile(filepath.Join(s.options.Workspace, "note.txt"), []byte("old"), 0600)
	w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"create","mode":"default"}`)
	var origin SessionView
	json.Unmarshal(w.Body.Bytes(), &origin)
	bound, _ := s.store.Workspace(t.Context(), origin.ID)
	scope = bound.ScopeID
	path := "/api/v1/sessions/" + origin.ID + "/jobs"
	jobs := []interaction.Job{}
	for i, content := range []string{"edit", "hold", "hold"} {
		w = apiRequest(s, "POST", path, fmt.Sprintf(`{"request_id":"job%d","content":%q,"effects":true}`, i, content))
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		var op interaction.JobOperationRecord
		json.Unmarshal(w.Body.Bytes(), &op)
		jobs = append(jobs, op.Job)
		state := "model"
		if i == 0 {
			state = "waiting_approval"
		}
		awaitState(t, s, op.Job.SessionID, state)
	}
	snap, _ := s.snapshot(t.Context(), jobs[0].SessionID)
	a := snap.Runs[0].Approval
	body := fmt.Sprintf(`{"request_id":"approve","approval_id":%q,"digest":%q,"decision":"approved"}`, a.ID, a.Digest)
	w = apiRequest(s, "POST", path+"/"+jobs[0].ID+"/decision", body)
	if w.Code != 503 {
		t.Fatal("approval exceeded Job capacity", w.Code, w.Body.String())
	}
	if _, e := s.store.JobOperation(t.Context(), origin.ID, "approve"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("rejected decision persisted", e)
	}
	w = apiRequest(s, "POST", path+"/"+jobs[1].ID+"/stop", `{"request_id":"stop"}`)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	awaitState(t, s, jobs[1].SessionID, "cancelled")
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		s.mu.Lock()
		n, _ := s.jobWorkerCount(t.Context())
		s.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	w = apiRequest(s, "POST", path+"/"+jobs[0].ID+"/decision", body)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	awaitState(t, s, jobs[0].SessionID, "completed")
}
func TestJobsAdvertiseOnlySupportedCommands(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	w := apiRequest(s, "GET", "/api/v1/status", "")
	var v struct {
		Commands []string `json:"commands"`
	}
	json.Unmarshal(w.Body.Bytes(), &v)
	found := false
	for _, command := range v.Commands {
		if command == "jobs" {
			found = true
		}
	}
	if !found {
		t.Fatal("Job controls missing from server command capabilities", v.Commands)
	}
}
func TestJobsOutOfOrderCompletionNeverEntersNewerQueuedTurn(t *testing.T) {
	release := make(chan struct{})
	laterContext := make(chan string, 1)
	p := testSelection()
	p.Provider = interactionStreamFunc(func(ctx context.Context, r model.TurnRequest, _ func(model.ModelEvent) error) (model.TurnResult, error) {
		input := r.Messages[len(r.Messages)-1].Content
		if input == "parent" {
			<-ctx.Done()
			return model.TurnResult{}, ctx.Err()
		}
		if input == "slow-job" {
			select {
			case <-release:
			case <-ctx.Done():
				return model.TurnResult{}, ctx.Err()
			}
		}
		if input == "later" {
			raw, _ := json.Marshal(r.Messages)
			laterContext <- string(raw)
			return model.TurnResult{Content: "origin reply"}, nil
		}
		return model.TurnResult{Content: "secret job result:" + input}, nil
	})
	s, _ := serviceFixture(t, p)
	id := createChat(t, s)
	path := "/api/v1/sessions/" + id
	apiRequest(s, "POST", path+"/messages", `{"request_id":"parent","content":"parent"}`)
	parent := awaitState(t, s, id, "model").Runs[0]
	jobs := []interaction.Job{}
	for i, text := range []string{"slow-job", "fast-job"} {
		w := apiRequest(s, "POST", path+"/jobs", fmt.Sprintf(`{"request_id":"j%d","content":%q,"origin_run_id":%q}`, i, text, parent.ID))
		if w.Code != 202 {
			t.Fatal(w.Body.String())
		}
		var op interaction.JobOperationRecord
		json.Unmarshal(w.Body.Bytes(), &op)
		jobs = append(jobs, op.Job)
	}
	awaitState(t, s, jobs[1].SessionID, "completed")
	first, _ := s.store.Job(t.Context(), jobs[0].ID)
	if first.State != "running" {
		t.Fatal("not independent/out of order", first)
	}
	w := apiRequest(s, "POST", path+"/queue", `{"request_id":"later","content":"later"}`)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	w = apiRequest(s, "POST", "/api/v1/runs/"+parent.ID+"/cancel", `{"request_id":"cancel-parent"}`)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	snap := awaitState(t, s, id, "completed")
	if len(snap.Runs) != 2 || snap.Runs[1].Input != "later" || snap.Runs[1].Text != "origin reply" {
		t.Fatal(snap)
	}
	select {
	case raw := <-laterContext:
		if strings.Contains(raw, "job") {
			t.Fatal("Job output/input injected", raw)
		}
	case <-time.After(time.Second):
		t.Fatal("no queued context")
	}
	close(release)
	awaitState(t, s, jobs[0].SessionID, "completed")
	after, _ := s.snapshot(t.Context(), id)
	if len(after.Runs) != 2 || after.Cursor != snap.Cursor {
		t.Fatal("completion mutated newer origin context", after)
	}
}
func TestJobsArtifactInspectionIsBoundedAndExecutionScoped(t *testing.T) {
	p := testSelection()
	p.ToolsSupported = true
	var scope string
	p.Provider = interactionStreamFunc(func(_ context.Context, r model.TurnRequest, _ func(model.ModelEvent) error) (model.TurnResult, error) {
		if r.Messages[len(r.Messages)-1].Role == "tool" {
			return model.TurnResult{Content: "read complete"}, nil
		}
		args, _ := json.Marshal(interaction.ReadArguments{Scope: scope, Path: "large.txt"})
		return model.TurnResult{ToolCalls: []model.ToolCall{{ID: "read", Name: "read", Arguments: string(args)}}}, nil
	})
	s, _ := serviceFixture(t, p)
	data := strings.Repeat("owned artifact\n", 600)
	os.WriteFile(filepath.Join(s.options.Workspace, "large.txt"), []byte(data), 0600)
	w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"create","mode":"default"}`)
	var origin SessionView
	json.Unmarshal(w.Body.Bytes(), &origin)
	bound, _ := s.store.Workspace(t.Context(), origin.ID)
	scope = bound.ScopeID
	path := "/api/v1/sessions/" + origin.ID + "/jobs"
	w = apiRequest(s, "POST", path, `{"request_id":"job","content":"read"}`)
	var op interaction.JobOperationRecord
	json.Unmarshal(w.Body.Bytes(), &op)
	snap := awaitState(t, s, op.Job.SessionID, "completed")
	var tool struct {
		Artifact struct {
			ID string `json:"artifact_id"`
		} `json:"artifact"`
	}
	if e := json.Unmarshal([]byte(snap.Runs[0].Tools[0].Result), &tool); e != nil || tool.Artifact.ID == "" {
		t.Fatal(snap, e)
	}
	w = apiRequest(s, "GET", path+"/"+op.Job.ID+"/artifacts/"+tool.Artifact.ID, "")
	if w.Code != 200 {
		t.Fatal("artifact not inspectable", w.Code, w.Body.String())
	}
	var result struct {
		Content   string `json:"content"`
		SessionID string `json:"session_id"`
		Encoding  string `json:"encoding"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Content != data || result.SessionID != op.Job.SessionID || result.Encoding != "utf8" {
		t.Fatal(result)
	}
	other, e := s.store.CreateJob(t.Context(), origin.ID, "other-job", "unrelated", "", false)
	if e != nil {
		t.Fatal(e)
	}
	w = apiRequest(s, "GET", path+"/"+other.ID+"/artifacts/"+tool.Artifact.ID, "")
	if w.Code != 404 {
		t.Fatal("artifact crossed Job execution scope", w.Code)
	}
	binaryID := fmt.Sprintf("%x", sha256.Sum256([]byte{255}))
	if _, e = s.runtime.DB.SQL.Exec("INSERT INTO interaction_artifacts VALUES(?,?,?)", op.Job.SessionID, binaryID, []byte{255}); e != nil {
		t.Fatal(e)
	}
	w = apiRequest(s, "GET", path+"/"+op.Job.ID+"/artifacts/"+binaryID, "")
	json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || result.Encoding != "base64" || result.Content != "/w==" {
		t.Fatal(w.Code, w.Body.String())
	}
	largeID := strings.Repeat("a", 64)
	if _, e = s.runtime.DB.SQL.Exec("INSERT INTO interaction_artifacts VALUES(?,?,?)", op.Job.SessionID, largeID, make([]byte, interaction.MaxWorkspaceFileBytes+1)); e != nil {
		t.Fatal(e)
	}
	w = apiRequest(s, "GET", path+"/"+op.Job.ID+"/artifacts/"+largeID, "")
	if w.Code != 409 {
		t.Fatal("unbounded artifact read", w.Code)
	}
	other, _ = s.store.Job(t.Context(), other.ID)
	if other.State != "registered" {
		t.Fatal("inspection launched unrelated Job")
	}
}
func TestJobsKnownMissingTargetsRejectBeforeMutation(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	id := createChat(t, s)
	base := "/api/v1/sessions/" + id + "/jobs"
	for _, tc := range []struct{ path, body string }{{base + "/missing/stop", `{"request_id":"missing-stop"}`}, {base, `{"request_id":"missing-origin-run","content":"work","origin_run_id":"missing"}`}} {
		w := apiRequest(s, "POST", tc.path, tc.body)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "job_operation_rejected") {
			t.Fatal("known precommit refusal quarantined as unknown", w.Code, w.Body.String())
		}
	}
	var n int
	s.runtime.DB.SQL.QueryRow("SELECT count(*) FROM interaction_job_operations").Scan(&n)
	if n != 0 {
		t.Fatal("invalid intent persisted")
	}
}

func TestJobsContextBudgetFailureReleasesCapacityWithoutExecution(t *testing.T) {
	p := testSelection()
	p.Provider = interactionStreamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		t.Error("budget-refused Job reached provider")
		return model.TurnResult{}, nil
	})
	s, _ := serviceFixture(t, p)
	id := createChat(t, s)
	path := "/api/v1/sessions/" + id + "/jobs"
	var first interaction.JobOperationRecord
	for i := 0; i < interaction.JobOriginLimit+1; i++ {
		body, _ := json.Marshal(map[string]string{"request_id": fmt.Sprintf("budget-%d", i), "content": strings.Repeat("x", 40000)})
		w := apiRequest(s, "POST", path, string(body))
		var op interaction.JobOperationRecord
		if e := json.Unmarshal(w.Body.Bytes(), &op); e != nil || w.Code != 202 {
			t.Fatal(w.Code, w.Body.String(), e)
		}
		if op.Job.State != "failed" || op.Job.BlockedReason != "context_budget_exceeded_shorten_job_input" {
			t.Fatal("known pre-start budget failure stranded", op.Job.State, op.Job.BlockedReason)
		}
		if _, e := s.store.EngineCheckpoint(t.Context(), op.Job.RunID); !errors.Is(e, sql.ErrNoRows) {
			t.Fatal("refusal created execution checkpoint", e)
		}
		if i == 0 {
			first = op
		}
	}
	w := apiRequest(s, "GET", "/api/v1/sessions/"+id+"/job-operations/budget-0", "")
	var recovered interaction.JobOperationRecord
	json.Unmarshal(w.Body.Bytes(), &recovered)
	if recovered.Job.ID != first.Job.ID || recovered.Job.State != "failed" {
		t.Fatal("failed identity not recoverable", w.Body.String())
	}
}

func TestJobsAmbiguousAdmissionWithOversizeInputRemainsUnknown(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	id := createChat(t, s)
	s.Close()
	j, e := s.store.CreateJob(t.Context(), id, "ambiguous-budget", strings.Repeat("x", 40000), "", false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.store.AdmitJob(t.Context(), j.ID); e != nil {
		t.Fatal(e)
	}
	next, e := NewInteractionService(rt, s.options)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	w := apiRequest(next, "POST", "/api/v1/sessions/"+id+"/jobs/"+j.ID+"/stop", `{"request_id":"ambiguous-stop"}`)
	var op interaction.JobOperationRecord
	json.Unmarshal(w.Body.Bytes(), &op)
	if w.Code != 202 || op.Job.State != "unknown" || op.Job.BlockedReason != "" {
		t.Fatal("ambiguous missing checkpoint reclassified", w.Code, w.Body.String())
	}
	if _, e = next.store.EngineCheckpoint(t.Context(), j.RunID); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("ambiguous checkpoint fabricated", e)
	}
}

func TestJobsPreparationFailureStorageErrorRemainsUnknown(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	id := createChat(t, s)
	// Refuse the final failure event, after both run and reason updates. The
	// transaction must roll back; a later kick cannot infer safe non-execution.
	_, e := s.runtime.DB.SQL.Exec(`CREATE TRIGGER refuse_job_failure BEFORE INSERT ON interaction_events WHEN NEW.type='run.failed' BEGIN SELECT RAISE(ABORT,'owned failure fixture'); END`)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := json.Marshal(map[string]string{"request_id": "failure-storage", "content": strings.Repeat("x", 40000)})
	path := "/api/v1/sessions/" + id + "/jobs"
	w := apiRequest(s, "POST", path, string(body))
	var first interaction.JobOperationRecord
	json.Unmarshal(w.Body.Bytes(), &first)
	if w.Code != 202 || first.Job.State != "unknown" || first.Job.BlockedReason != "" {
		t.Fatal("partial failure transaction", w.Code, w.Body.String())
	}
	if _, e = s.runtime.DB.SQL.Exec("DROP TRIGGER refuse_job_failure"); e != nil {
		t.Fatal(e)
	}
	w = apiRequest(s, "POST", path, string(body))
	var replay interaction.JobOperationRecord
	json.Unmarshal(w.Body.Bytes(), &replay)
	if w.Code != 202 || replay.Job.ID != first.Job.ID || replay.Job.State != "unknown" {
		t.Fatal("storage failure replayed", w.Body.String())
	}
}
