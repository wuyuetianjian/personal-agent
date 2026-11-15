package api

import (
	"encoding/json"
	"testing"
)

func TestTrustedWorkspaceStatusDefaultOff(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	w := apiRequest(s, "GET", "/api/v1/status", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v struct {
		Trusted *struct {
			Enabled bool
			Reason  string
		} `json:"trusted_workspace"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	if v.Trusted == nil || v.Trusted.Enabled || v.Trusted.Reason == "" {
		t.Fatalf("missing explicit default-off trusted workspace status: %s", w.Body.String())
	}
}
func TestTrustedWorkspaceRejectsUnconfiguredHostBeforeAdmission(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	for _, mode := range []string{"default", "plan"} {
		w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"trusted-`+mode+`","mode":"`+mode+`","trusted_workspace":true}`)
		if w.Code != 409 {
			t.Fatalf("host gate: %d %s", w.Code, w.Body.String())
		}
		var out struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out.Error.Code != "trusted_workspace_unavailable" {
			t.Fatal(w.Body.String())
		}
	}
	for _, table := range []string{"interaction_sessions", "interaction_workspaces", "interaction_mcp_admissions"} {
		var n int
		if e := rt.DB.SQL.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil || n != 0 {
			t.Fatalf("rejected trust left %s: %d %v", table, n, e)
		}
	}
}
func TestTrustedWorkspaceRefusesIncompatibleModes(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	for _, extra := range []string{`"mode":"chat_only"`, `"mode":"acceptEdits"`, `"mode":"default","mcp":true`} {
		w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"invalid-trusted","trusted_workspace":true,`+extra+`}`)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
		var out struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out.Error.Code != "trusted_workspace_read_only_required" {
			t.Fatal(w.Body.String())
		}
	}
}

func TestTrustedWorkspaceReplayCannotChangeIntent(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"replay-trust","mode":"chat_only"}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var body map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil {
		t.Fatal(e)
	}
	body["trusted_workspace"] = true
	raw, _ := json.Marshal(body)
	if e := s.validateSessionReplay(t.Context(), writeInput{RequestID: "replay-trust", Mode: "chat_only"}, raw); e == nil {
		t.Fatal("stored replay silently changed workspace trust")
	}
}
