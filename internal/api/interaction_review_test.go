package api

import (
	"agent/internal/interaction"
	"agent/internal/model"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func namedSession(t *testing.T, s *InteractionService, name, mode string) string {
	t.Helper()
	w := apiRequest(s, "POST", "/api/v1/sessions", fmt.Sprintf(`{"request_id":%q,"mode":%q}`, "create-"+name, mode))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v SessionView
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	return v.ID
}
func postReviewMessage(t *testing.T, s *InteractionService, id, request, content string) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"request_id": request, "content": content})
	w := apiRequest(s, "POST", "/api/v1/sessions/"+id+"/messages", string(raw))
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v struct {
		ID string `json:"run_id"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	return v.ID
}
func reviewProvider(entered chan<- struct{}, release <-chan struct{}) ProviderSelection {
	p := testSelection()
	p.ToolsSupported = true
	p.Provider = interactionStreamFunc(func(ctx context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
		input, scope := "", ""
		for _, m := range r.Messages {
			if m.Role == "user" {
				input = m.Content
			}
			if strings.HasPrefix(m.Content, "Workspace scope: ") {
				scope = strings.Split(strings.TrimPrefix(m.Content, "Workspace scope: "), ".")[0]
			}
		}
		if input == "hold" {
			entered <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return model.TurnResult{}, ctx.Err()
			}
			return model.TurnResult{Content: "released"}, nil
		}
		if len(r.Messages) > 0 && r.Messages[len(r.Messages)-1].Role == "tool" {
			return model.TurnResult{Content: "observed result, done"}, nil
		}
		if input == "edit" {
			hash := sha256.Sum256([]byte("old"))
			args, _ := json.Marshal(interaction.PatchArguments{Scope: scope, Path: "note.txt", Preimage: fmt.Sprintf("%x", hash), Content: "new"})
			return model.TurnResult{ToolCalls: []model.ToolCall{{ID: "edit", Name: "apply_patch", Arguments: string(args)}}}, nil
		}
		return model.TurnResult{Content: "done"}, nil
	})
	return p
}
func waitReview(t *testing.T, description string, f func() bool) {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("not reached:", description)
}
func TestSaturatedServiceRejectsNewAdmissionAndDrainsAcknowledgedCancel(t *testing.T) {
	for _, variant := range []string{"new_message", "paused_cancel"} {
		t.Run(variant, func(t *testing.T) {
			entered := make(chan struct{}, 4)
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			s, rt := serviceFixture(t, reviewProvider(entered, release))
			defer unblock()
			var paused, run string
			if variant == "paused_cancel" {
				if !s.broker.Capabilities().Files.Enabled {
					t.Skip("native file adapter unavailable")
				}
				if e := os.WriteFile(filepath.Join(s.options.Workspace, "note.txt"), []byte("old"), 0600); e != nil {
					t.Fatal(e)
				}
				paused = namedSession(t, s, "paused", "default")
				run = postReviewMessage(t, s, paused, "edit", "edit")
				awaitState(t, s, paused, "waiting_approval")
				waitReview(t, "paused owner released", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.workers) == 0 })
			}
			for i := 0; i < 4; i++ {
				id := namedSession(t, s, fmt.Sprintf("hold%d", i), "chat_only")
				postReviewMessage(t, s, id, fmt.Sprintf("hold%d", i), "hold")
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("callback not entered")
				}
			}
			if variant == "new_message" {
				id := namedSession(t, s, "fifth", "chat_only")
				w := apiRequest(s, "POST", "/api/v1/sessions/"+id+"/messages", `{"request_id":"fifth-message","content":"hello"}`)
				if w.Code != 503 || !strings.Contains(w.Body.String(), "service_capacity") {
					t.Fatal("saturated admission accepted", w.Code, w.Body.String())
				}
				var n int
				rt.DB.SQL.QueryRow("SELECT count(*) FROM interaction_runs WHERE session_id=?", id).Scan(&n)
				if n != 0 {
					t.Fatal("rejected message was persisted")
				}
				unblock()
				return
			}
			w := apiRequest(s, "POST", "/api/v1/runs/"+run+"/cancel", `{"request_id":"cancel-paused"}`)
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			waitReview(t, "visible capacity wait", func() bool {
				evs, e := s.store.Events(context.Background(), paused, 0, 1000)
				if e != nil {
					t.Fatal(e)
				}
				for _, ev := range evs {
					if ev.Type == "run.waiting_capacity" {
						return true
					}
				}
				return false
			})
			snap, e := s.snapshot(context.Background(), paused)
			if e != nil || snap.Runs[0].Phase != "cancelling" {
				t.Fatal(snap, e)
			}
			unblock()
			awaitState(t, s, paused, "cancelled")
			contents, e := os.ReadFile(filepath.Join(s.options.Workspace, "note.txt"))
			if e != nil || string(contents) != "old" {
				t.Fatal("cancelled edit executed", string(contents), e)
			}
		})
	}
}

func TestStartupRecoveryPublishesCompletedJournalObservationExactlyOnce(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled=%t", cancelled), func(t *testing.T) {
			p := reviewProvider(nil, nil)
			s, rt := serviceFixture(t, p)
			if !s.broker.Capabilities().Files.Enabled {
				t.Skip("native file adapter unavailable")
			}
			ctx := context.Background()
			path := filepath.Join(s.options.Workspace, "note.txt")
			if e := os.WriteFile(path, []byte("old"), 0600); e != nil {
				t.Fatal(e)
			}
			id := namedSession(t, s, "recover", "default")
			run := postReviewMessage(t, s, id, "edit", "edit")
			paused := awaitState(t, s, id, "waiting_approval")
			approval := paused.Runs[0].Approval
			if approval == nil {
				t.Fatal("missing approval")
			}
			// Real native Execute and journal.Complete succeed; fail only later checkpoint
			// and terminal commits to reproduce abrupt loss at that exact crash boundary.
			for _, statement := range []string{`CREATE TEMP TRIGGER interrupt_tool_save BEFORE INSERT ON interaction_events WHEN NEW.type='tool.completed' BEGIN SELECT RAISE(ABORT,'simulated crash after completed journal'); END`, `CREATE TEMP TRIGGER interrupt_terminal BEFORE UPDATE ON interaction_runs WHEN NEW.state<>'running' BEGIN SELECT RAISE(ABORT,'simulated crash before terminal'); END`} {
				if _, e := rt.DB.SQL.Exec(statement); e != nil {
					t.Fatal(e)
				}
			}
			body, _ := json.Marshal(map[string]string{"request_id": "approve", "digest": approval.Digest, "decision": "approved"})
			w := apiRequest(s, "POST", "/api/v1/approvals/"+approval.ID+"/decision", string(body))
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			waitReview(t, "completed journal and released crashed owner", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.workers) == 0 })
			cp, e := s.store.EngineCheckpoint(ctx, run)
			if e != nil || len(cp.Pending) != 1 {
				t.Fatal(cp, e)
			}
			var record string
			if e = rt.DB.SQL.QueryRow("SELECT result_ref FROM side_effect_records WHERE workflow_id=? AND status='completed'", run).Scan(&record); e != nil {
				t.Fatal(e)
			}
			var observed interaction.ToolResult
			if e = json.Unmarshal([]byte(record), &observed); e != nil {
				t.Fatal(e)
			}
			if cancelled {
				engine, e := interaction.NewEngine(s.store, offlineProvider{}, s.broker, interaction.EngineOptions{})
				if e != nil {
					t.Fatal(e)
				}
				if e = engine.Cancel(ctx, run); e != nil {
					t.Fatal(e)
				}
			}
			before, e := s.snapshot(ctx, id)
			if e != nil {
				t.Fatal(e)
			}
			if before.Runs[0].Tools[0].State != "executing" {
				t.Fatal("wrong crash boundary", before)
			}
			options := s.options
			parent := filepath.Dir(rt.Directory)
			s.Close()
			if e = rt.Close(); e != nil {
				t.Fatal(e)
			}
			reopened, e := OpenInteractionRuntime(ctx, parent)
			if e != nil {
				t.Fatal(e)
			}
			defer reopened.Close()
			recovered, e := NewInteractionService(reopened, options)
			if e != nil {
				t.Fatal(e)
			}
			defer recovered.Close()
			after, e := recovered.snapshot(ctx, id)
			if e != nil {
				t.Fatal(e)
			}
			tool := after.Runs[0].Tools[0]
			if tool.State != "completed" || tool.Result != observed.Content || tool.IsError != observed.IsError {
				t.Fatal("completed observation absent from snapshot", tool, observed)
			}
			if cancelled && after.Runs[0].State != "cancelled" {
				t.Fatal(after)
			}
			// Replay the actual SSE response from the pre-crash cursor into the old view.
			requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			req := httptest.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:7319/api/v1/sessions/%s/events?after=%d", id, before.Cursor), nil).WithContext(requestCtx)
			req.RemoteAddr = "127.0.0.1:1000"
			req.Header.Set("Authorization", "Bearer secret-token")
			recorder := &recoverySSERecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
			recovered.ServeHTTP(recorder, req)
			view := before.Runs[0]
			count := 0
			for _, line := range strings.Split(recorder.Body.String(), "\n") {
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				var event WireEvent
				if e = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); e != nil {
					t.Fatal(e)
				}
				if event.Type == "tool.recovered" {
					count++
				}
				applyWire(&view, event)
			}
			if count != 1 || view.Tools[0].State != "completed" || view.Tools[0].Result != observed.Content {
				t.Fatal("SSE result missing", count, view)
			}
			// Repeated recovery/resume must neither emit another observation nor execute.
			engine, e := interaction.NewEngine(recovered.store, p.Provider, recovered.broker, interaction.EngineOptions{})
			if e != nil {
				t.Fatal(e)
			}
			if e = engine.Recover(ctx, run); e != nil {
				t.Fatal(e)
			}
			if !cancelled {
				w = apiRequest(recovered, "POST", "/api/v1/runs/"+run+"/resume", `{"request_id":"resume"}`)
				if w.Code != 202 {
					t.Fatal(w.Code, w.Body.String())
				}
				awaitState(t, recovered, id, "completed")
			}
			var recoveredEvents, startedEvents int
			reopened.DB.SQL.QueryRow("SELECT count(*) FROM interaction_events WHERE run_id=? AND type='tool.recovered'", run).Scan(&recoveredEvents)
			reopened.DB.SQL.QueryRow("SELECT count(*) FROM interaction_events WHERE run_id=? AND type='tool.started'", run).Scan(&startedEvents)
			if recoveredEvents != 1 || startedEvents != 1 {
				t.Fatal("repeated observation or execution", recoveredEvents, startedEvents)
			}
			contents, e := os.ReadFile(path)
			if e != nil || string(contents) != "new" {
				t.Fatal(string(contents), e)
			}
		})
	}
}

type recoverySSERecorder struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w *recoverySSERecorder) Flush() {
	w.ResponseRecorder.Flush()
	if strings.Contains(w.Body.String(), `"type":"tool.recovered"`) {
		w.cancel()
	}
}

func TestPendingDrainQueueBoundRejectsBeforeCancelIntent(t *testing.T) {
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	s, rt := serviceFixture(t, reviewProvider(entered, release))
	defer unblock()
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		id := namedSession(t, s, fmt.Sprintf("active%d", i), "chat_only")
		postReviewMessage(t, s, id, fmt.Sprintf("active%d", i), "hold")
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("callback not entered")
		}
	}
	var queuedSessions []string
	for i := 0; i < maxInteractionWorkers-4; i++ {
		id := namedSession(t, s, fmt.Sprintf("queued%d", i), "chat_only")
		run, e := s.store.CreateRun(ctx, id, fmt.Sprintf("admitted%d", i), interaction.Message{Content: "saved but not yet started"})
		if e != nil {
			t.Fatal(e)
		}
		w := apiRequest(s, "POST", "/api/v1/runs/"+run.ID+"/cancel", fmt.Sprintf(`{"request_id":%q}`, fmt.Sprintf("cancel%d", i)))
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		queuedSessions = append(queuedSessions, id)
	}
	id := namedSession(t, s, "overflow", "chat_only")
	run, e := s.store.CreateRun(ctx, id, "overflow-admitted", interaction.Message{Content: "must remain uncancelled"})
	if e != nil {
		t.Fatal(e)
	}
	w := apiRequest(s, "POST", "/api/v1/runs/"+run.ID+"/cancel", `{"request_id":"overflow-cancel"}`)
	if w.Code != 503 {
		t.Fatal("unbounded drain admission", w.Code, w.Body.String())
	}
	var n int
	if e = rt.DB.SQL.QueryRow("SELECT count(*) FROM interaction_engine WHERE run_id=? AND cancel_requested=1", run.ID).Scan(&n); e != nil || n != 0 {
		t.Fatal("rejected cancel persisted intent", n, e)
	}
	unblock()
	for _, session := range queuedSessions {
		awaitState(t, s, session, "cancelled")
	}
}
