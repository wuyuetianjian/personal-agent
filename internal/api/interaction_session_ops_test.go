package api

import (
	"agent/internal/interaction"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSessionOperationsAuthenticatedScopedAndDurable(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	id := createChat(t, s)
	r, e := s.store.CreateRun(s.ctx, id, "input", interaction.Message{Content: "Never transmit"})
	if e != nil {
		t.Fatal(e)
	}
	s.store.Transition(s.ctx, r.ID, r.Version, interaction.RunFailed)
	snap, e := s.snapshotBefore(s.ctx, id, 0)
	if e != nil {
		t.Fatal(e)
	}
	body := fmt.Sprintf(`{"request_id":"branch","boundary_run_id":%q,"expected_cursor":%d}`, r.ID, snap.Cursor)
	path := "/api/v1/sessions/" + id + "/branch"
	w := apiRequest(s, "POST", path, body)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v SessionView
	if e = json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	if v.Provenance == nil || v.Provenance.ParentSessionID != id {
		t.Fatal(v)
	}
	// Erase only HTTP response cache to simulate a crash between domain commit and response ledger.
	rt.DB.SQL.Exec("DELETE FROM interaction_api_actions WHERE request_id='branch'")
	w = apiRequest(s, "POST", path, body)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var duplicate SessionView
	json.Unmarshal(w.Body.Bytes(), &duplicate)
	if duplicate.ID != v.ID {
		t.Fatal("duplicate child")
	}
	w = apiRequest(s, "POST", "/api/v1/sessions/"+v.ID+"/compact", `{"request_id":"compact","expected_cursor":0}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = apiRequest(s, "GET", "/api/v1/sessions/"+v.ID+"/context-view", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var view interaction.SessionContextView
	json.Unmarshal(w.Body.Bytes(), &view)
	if view.Selection != "compacted" || view.Original[0].Content != "Never transmit" {
		t.Fatal(view)
	}
	w = apiRequest(s, "POST", "/api/v1/sessions/"+v.ID+"/compact", `{"request_id":"original","expected_cursor":0,"selection":"original"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = apiRequest(s, "POST", path, `{"request_id":"bad","boundary_run_id":"foreign","expected_cursor":2}`)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = apiRequest(s, "POST", path, `{"request_id":"bad","boundary_run_id":"foreign","expected_cursor":2,"root":"/tmp"}`)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestContextOperationReadOnlyRecoveryIsScopedAndSurvivesRestart(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	id := createChat(t, s)
	response := apiRequest(s, "POST", "/api/v1/sessions/"+id+"/clear", `{"request_id":"recover-me","expected_cursor":0}`)
	if response.Code != 201 {
		t.Fatal(response.Code, response.Body.String())
	}
	var child SessionView
	json.Unmarshal(response.Body.Bytes(), &child)
	// The domain ledger, not the optional HTTP response cache, is recovery authority.
	rt.DB.SQL.Exec("DELETE FROM interaction_api_actions WHERE request_id='recover-me'")
	response = apiRequest(s, "GET", "/api/v1/sessions/"+id+"/operations/recover-me", "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var v interaction.SessionOperationRecord
	json.Unmarshal(response.Body.Bytes(), &v)
	if v.Operation.RequestID != "recover-me" || v.Operation.Kind != "clear" || v.Provenance == nil || v.Provenance.SessionID != child.ID {
		t.Fatal(v)
	}
	if got := apiRequest(s, "GET", "/api/v1/sessions/"+child.ID+"/operations/recover-me", ""); got.Code != 404 {
		t.Fatal("cross-session lookup", got.Code)
	}
	if got := apiRequest(s, "GET", "/api/v1/sessions/"+id+"/operations/not-recorded", ""); got.Code != 404 {
		t.Fatal("unknown lookup", got.Code)
	}
}

func TestSessionOperationConflictIsExplicitPrecommitRejection(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	id := createChat(t, s)
	w := apiRequest(s, "POST", "/api/v1/sessions/"+id+"/branch", `{"request_id":"bad-boundary","expected_cursor":0,"boundary_run_id":"missing"}`)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "context_operation_rejected") {
		t.Fatal(w.Code, w.Body.String())
	}
}
