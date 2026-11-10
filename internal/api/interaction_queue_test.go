package api

import (
	"agent/internal/interaction"
	"agent/internal/model"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInputQueueExplicitFIFOAndWithdrawal(t *testing.T) {
	entered := make(chan string, 4)
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	p := testSelection()
	p.Provider = interactionStreamFunc(func(ctx context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
		text := r.Messages[len(r.Messages)-1].Content
		entered <- text
		if text == "active" {
			select {
			case <-release:
			case <-ctx.Done():
				return model.TurnResult{}, ctx.Err()
			}
		}
		return model.TurnResult{Content: "done"}, nil
	})
	s, _ := serviceFixture(t, p)
	id := createChat(t, s)
	path := "/api/v1/sessions/" + id
	w := apiRequest(s, "POST", path+"/messages", `{"request_id":"active","content":"active"}`)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	<-entered
	for _, request := range []string{"a", "b", "withdraw"} {
		w = apiRequest(s, "POST", path+"/queue", fmt.Sprintf(`{"request_id":%q,"content":%q}`, request, request))
		if w.Code != 202 {
			t.Fatal("enqueue", w.Code, w.Body.String())
		}
	}
	w = apiRequest(s, "POST", path+"/queue", `{"request_id":"a","content":"a"}`)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	var q interaction.QueueView
	w = apiRequest(s, "GET", path+"/queue", "")
	if json.Unmarshal(w.Body.Bytes(), &q) != nil || len(q.Items) != 3 || q.Items[1].Position != 2 {
		t.Fatal(w.Body.String())
	}
	if q.Items[0].BlockedReason == "" {
		t.Fatal("invisible block")
	}
	w = apiRequest(s, "POST", path+"/queue/"+q.Items[2].ID+"/withdraw", `{"request_id":"w"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = apiRequest(s, "POST", path+"/messages", `{"request_id":"implicit","content":"should stay draft"}`); w.Code != 409 {
		t.Fatal("implicit send accepted")
	}
	once.Do(func() { close(release) })
	for _, want := range []string{"a", "b"} {
		select {
		case got := <-entered:
			if got != want {
				t.Fatal(got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("queue did not run", want)
		}
	}
	snap := awaitState(t, s, id, "completed")
	if len(snap.Runs) != 3 {
		t.Fatal(snap)
	}
	// Loss of generic acknowledgement does not erase domain idempotency.
	s.runtime.DB.SQL.Exec("DELETE FROM interaction_api_actions WHERE request_id='a'")
	w = apiRequest(s, "POST", path+"/queue", `{"request_id":"a","content":"a"}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = apiRequest(s, "POST", path+"/messages", `{"request_id":"a","content":"a"}`); w.Code != 409 {
		t.Fatal("request ID crossed routes")
	}
	if w = apiRequest(s, "POST", path+"/queue", `{"request_id":"a","content":"changed"}`); w.Code != 409 {
		t.Fatal("mutated retry")
	}
	if w = apiRequest(s, "GET", path+"/queue-operations/a", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
}
func TestInputQueueUncertainAdmissionBlocksAfterRestart(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	id := createChat(t, s)
	q, e := s.store.Enqueue(t.Context(), id, "queued", interaction.Message{Content: "never replay"})
	if e != nil {
		t.Fatal(e)
	}
	run, e := s.store.AdmitQueue(t.Context(), q.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.store.Enqueue(t.Context(), id, "next", interaction.Message{Content: "blocked"})
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	next, e := NewInteractionService(rt, s.options)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	time.Sleep(30 * time.Millisecond)
	list, e := next.store.Queue(t.Context(), id)
	if e != nil || list.Items[0].RunID != run.ID || list.Items[0].Phase != "unknown_admission" || list.Items[1].BlockedReason != "previous_run:unknown_admission" {
		t.Fatal(list, e)
	}
	var n int
	rt.DB.SQL.QueryRow("SELECT count(*) FROM interaction_runs WHERE session_id=?", id).Scan(&n)
	if n != 1 {
		t.Fatal("replayed", n)
	}
}

func TestInputQueueApprovalOriginAndPlan(t *testing.T) {
	for _, mode := range []string{"default", "plan"} {
		t.Run(mode, func(t *testing.T) {
			p := testSelection()
			p.ToolsSupported = true
			var scope string
			p.Provider = interactionStreamFunc(func(ctx context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
				last := r.Messages[len(r.Messages)-1]
				if last.Role == "tool" || last.Content != "edit" {
					return model.TurnResult{Content: "done"}, nil
				}
				hash := sha256.Sum256([]byte("old\n"))
				args, _ := json.Marshal(interaction.PatchArguments{Scope: scope, Path: "note.txt", Preimage: fmt.Sprintf("%x", hash), Content: "new\n"})
				return model.TurnResult{ToolCalls: []model.ToolCall{{ID: "call", Name: "apply_patch", Arguments: string(args)}}}, nil
			})
			s, _ := serviceFixture(t, p)
			os.WriteFile(filepath.Join(s.options.Workspace, "note.txt"), []byte("old\n"), 0600)
			w := apiRequest(s, "POST", "/api/v1/sessions", fmt.Sprintf(`{"request_id":"create","mode":%q}`, mode))
			if w.Code != 201 {
				t.Fatal(w.Body.String())
			}
			var session SessionView
			json.Unmarshal(w.Body.Bytes(), &session)
			binding, _ := s.store.Workspace(t.Context(), session.ID)
			scope = binding.ScopeID
			path := "/api/v1/sessions/" + session.ID
			w = apiRequest(s, "POST", path+"/queue", `{"request_id":"edit","content":"edit"}`)
			if w.Code != 202 {
				t.Fatal(w.Body.String())
			}
			var op interaction.QueueOperationRecord
			json.Unmarshal(w.Body.Bytes(), &op)
			if mode == "default" {
				snap := awaitState(t, s, session.ID, "waiting_approval")
				approval := snap.Runs[0].Approval
				if approval == nil {
					t.Fatal(snap)
				}
				a, _ := s.store.GetApproval(t.Context(), approval.ID)
				if a.RunID != op.Item.RunID || snap.Runs[0].Input != "edit" {
					t.Fatal("wrong approval origin", a, op)
				}
				if w = apiRequest(s, "POST", path+"/queue", `{"request_id":"later","content":"later does not approve"}`); w.Code != 202 {
					t.Fatal(w.Body.String())
				}
				a, _ = s.store.GetApproval(t.Context(), approval.ID)
				if a.Decision != "pending" {
					t.Fatal("queue changed approval")
				}
				w = apiRequest(s, "POST", "/api/v1/approvals/"+a.ID+"/decision", fmt.Sprintf(`{"request_id":"deny","digest":%q,"decision":"denied"}`, a.ActionDigest))
				if w.Code != 202 {
					t.Fatal(w.Body.String())
				}
				awaitState(t, s, session.ID, "completed")
			} else {
				awaitState(t, s, session.ID, "failed")
			}
			raw, _ := os.ReadFile(filepath.Join(s.options.Workspace, "note.txt"))
			if string(raw) != "old\n" {
				t.Fatal("unapproved/plan write", string(raw))
			}
		})
	}
}
func TestInputQueueProviderRevocationStickyAndPendingRestart(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	id := createChat(t, s)
	q, e := s.store.Enqueue(t.Context(), id, "q", interaction.Message{Content: "safe"})
	if e != nil {
		t.Fatal(e)
	}
	original := s.options.Resolve
	s.options.Resolve = func(context.Context, string) (ProviderSelection, error) {
		p := testSelection()
		p.Endpoint = "http://127.0.0.1:9000/changed"
		return p, nil
	}
	s.mu.Lock()
	s.kickQueues()
	s.mu.Unlock()
	list, _ := s.store.Queue(t.Context(), id)
	if list.Items[0].State != "pending" || !strings.Contains(list.Items[0].BlockedReason, "provider") {
		t.Fatal(list)
	}
	s.options.Resolve = original
	s.mu.Lock()
	s.kickQueues()
	s.mu.Unlock()
	list, _ = s.store.Queue(t.Context(), id)
	if list.Items[0].State != "pending" {
		t.Fatal("automatically reinterpreted")
	}
	if _, e = s.store.WithdrawQueue(t.Context(), id, q.ID, "withdraw"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.store.Enqueue(t.Context(), id, "fresh", interaction.Message{Content: "safe"}); e != nil {
		t.Fatal(e)
	}
	s.Close()
	next, e := NewInteractionService(rt, s.options)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	snap := awaitState(t, next, id, "completed")
	if len(snap.Runs) != 1 {
		t.Fatal(snap)
	}
}

func TestInputQueueEscapedRequestRecovery(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	id := createChat(t, s)
	for _, request := range []string{"nested/id/", "百分比% #?"} {
		body, _ := json.Marshal(map[string]string{"request_id": request, "content": "original"})
		w := apiRequest(s, "POST", "/api/v1/sessions/"+id+"/queue", string(body))
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		w = apiRequest(s, "GET", "/api/v1/sessions/"+id+"/queue-operations/"+url.PathEscape(request), "")
		if w.Code != 200 {
			t.Fatal("unrecoverable accepted request ID", request, w.Code, w.Body.String())
		}
		var record interaction.QueueOperationRecord
		json.Unmarshal(w.Body.Bytes(), &record)
		if record.RequestID != request {
			t.Fatal(record)
		}
	}
}

func TestInputQueueExecHostRevocationDoesNotReinterpret(t *testing.T) {
	rt, e := OpenInteractionRuntime(t.Context(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer rt.Close()
	options := InteractionOptions{Token: "secret-token", Host: "127.0.0.1:7319", Workspace: t.TempDir(), DefaultModel: "test", NonSandboxExec: true, Resolve: func(context.Context, string) (ProviderSelection, error) {
		p := testSelection()
		p.ToolsSupported = true
		return p, nil
	}}
	s, e := NewInteractionService(rt, options)
	if e != nil {
		t.Skip("native exec capability unavailable", e)
	}
	w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"create","mode":"default"}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var v SessionView
	json.Unmarshal(w.Body.Bytes(), &v)
	if _, e = s.store.Enqueue(t.Context(), v.ID, "q", interaction.Message{Content: "original effects policy"}); e != nil {
		t.Fatal(e)
	}
	s.Close()
	options.NonSandboxExec = false
	next, e := NewInteractionService(rt, options)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	list, e := next.store.Queue(t.Context(), v.ID)
	if e != nil || list.Items[0].State != "pending" || !strings.Contains(list.Items[0].BlockedReason, "exec") {
		t.Fatal("revoked policy reinterpreted", list, e)
	}
}

func TestInputQueueReplayReadFailureIsNotDefiniteRejection(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	id := createChat(t, s)
	if _, e := s.store.Enqueue(t.Context(), id, "q", interaction.Message{Content: "original"}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.runtime.DB.SQL.Exec("DROP TABLE interaction_session_branches"); e != nil {
		t.Fatal(e)
	}
	w := apiRequest(s, "POST", "/api/v1/sessions/"+id+"/queue", `{"request_id":"q","content":"original"}`)
	if w.Code != 500 || strings.Contains(w.Body.String(), "queue_operation_rejected") {
		t.Fatal("committed original operation was incorrectly released", w.Code, w.Body.String())
	}
}

func TestInputQueueSharesBoundedWorkersAcrossSessions(t *testing.T) {
	entered := make(chan struct{}, 8)
	p := testSelection()
	p.Provider = interactionStreamFunc(func(ctx context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return model.TurnResult{}, ctx.Err()
	})
	s, _ := serviceFixture(t, p)
	ids := []string{}
	for i := 0; i < 5; i++ {
		w := apiRequest(s, "POST", "/api/v1/sessions", fmt.Sprintf(`{"request_id":"session%d","mode":"chat_only"}`, i))
		var v SessionView
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatal(w.Body.String())
		}
		ids = append(ids, v.ID)
		w = apiRequest(s, "POST", "/api/v1/sessions/"+v.ID+"/queue", fmt.Sprintf(`{"request_id":"q%d","content":"wait"}`, i))
		if w.Code != 202 {
			t.Fatal(w.Body.String())
		}
	}
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("worker missing")
		}
	}
	s.mu.Lock()
	workers := len(s.workers)
	s.mu.Unlock()
	if workers != 4 {
		t.Fatal(workers)
	}
	list, _ := s.store.Queue(t.Context(), ids[4])
	if list.Items[0].State != "pending" || list.Items[0].BlockedReason != "waiting_capacity" {
		t.Fatal(list)
	}
	first, _ := s.store.Queue(t.Context(), ids[0])
	w := apiRequest(s, "POST", "/api/v1/runs/"+first.Items[0].RunID+"/cancel", `{"request_id":"cancel"}`)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("released capacity did not admit FIFO head")
	}
	list, _ = s.store.Queue(t.Context(), ids[4])
	if list.Items[0].State != "admitted" {
		t.Fatal(list)
	}
}
