//go:build !linux

package api

import (
	"agent/internal/interaction"
	"agent/internal/model"
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

func unavailableChatSession(t *testing.T, s *InteractionService, request string) SessionView {
	t.Helper()
	w := apiRequest(s, "POST", "/api/v1/sessions", fmt.Sprintf(`{"request_id":%q,"mode":"chat_only"}`, request))
	if w.Code != 201 {
		t.Fatalf("chat-only admission, before SessionView decode: %d %s", w.Code, w.Body.String())
	}
	var v SessionView
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	if v.ID == "" || v.Mode != "chat_only" || v.Files || v.Tools || v.NonSandboxExec {
		t.Fatalf("invalid chat-only authority %+v", v)
	}
	return v
}
func unavailableCount(t *testing.T, rt *InteractionRuntime, table string) int {
	t.Helper()
	var n int
	if e := rt.DB.SQL.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func unavailableChatRound(t *testing.T, s *InteractionService, id string) {
	t.Helper()
	w := apiRequest(s, "POST", "/api/v1/sessions/"+id+"/messages", `{"request_id":"message","content":"synthetic hello"}`)
	if w.Code != 202 {
		t.Fatalf("chat message admission: %d %s", w.Code, w.Body.String())
	}
	snap := awaitState(t, s, id, "completed")
	if len(snap.Runs) != 1 || snap.Runs[0].Text != "你好" || len(snap.Runs[0].Tools) != 0 || snap.Runs[0].Approval != nil {
		t.Fatalf("chat result %+v", snap)
	}
	w = apiRequest(s, "GET", "/api/v1/sessions/"+id+"/history", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var history Snapshot
	if e := json.Unmarshal(w.Body.Bytes(), &history); e != nil {
		t.Fatal(e)
	}
	if len(history.Runs) != 1 || history.Runs[0].ID != snap.Runs[0].ID || history.Runs[0].Input != "synthetic hello" || history.Runs[0].Text != "你好" {
		t.Fatalf("stored history %+v", history)
	}
}
func TestUnavailableNativeChatStreamAndHistory(t *testing.T) {
	var streams atomic.Int32
	selection := testSelection()
	selection.ToolsSupported = true
	original := selection.Provider
	selection.Provider = interactionStreamFunc(func(ctx context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
		streams.Add(1)
		if len(r.Tools) != 0 {
			t.Error("chat-only provider received native tools")
		}
		return original.Stream(ctx, r, emit)
	})
	s, rt := serviceFixture(t, selection)
	if s.broker.Capabilities().Files.Enabled || s.broker.Capabilities().Exec.Enabled || interaction.NativeMCPStdioCapability().Enabled {
		t.Fatal("native capability unexpectedly enabled")
	}
	v := unavailableChatSession(t, s, "chat")
	unavailableChatRound(t, s, v.ID)
	if streams.Load() != 1 {
		t.Fatal("provider stream count", streams.Load())
	}
	for _, table := range []string{"interaction_workspaces", "interaction_tool_proposals", "interaction_artifacts"} {
		if n := unavailableCount(t, rt, table); n != 0 {
			t.Fatal("chat gained native effects", table, n)
		}
	}
}
func TestUnavailableNativeStoredSkillOwnership(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	owner := unavailableChatSession(t, s, "owner")
	other := unavailableChatSession(t, s, "other")
	s.options.Resolve = func(context.Context, string) (ProviderSelection, error) {
		t.Error("stored metadata resolved a live provider")
		return ProviderSelection{}, errors.New("must remain stored-only")
	}
	path := "/api/v1/sessions/" + owner.ID + "/skills"
	w := apiRequest(s, "POST", path+"/validate", fmt.Sprintf(`{"manifest_text":%q}`, publicSkillManifest))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	body, e := json.Marshal(interaction.SkillIntent{RequestID: "preview", Kind: "import", ManifestText: publicSkillManifest})
	if e != nil {
		t.Fatal(e)
	}
	w = apiRequest(s, "POST", path+"/preview", string(body))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var preview interaction.SkillOperation
	if e = json.Unmarshal(w.Body.Bytes(), &preview); e != nil {
		t.Fatal(e)
	}
	if preview.Digest == "" || preview.CatalogID == "" || preview.RevisionID == "" {
		t.Fatal("incomplete preview", preview)
	}
	if n := unavailableCount(t, rt, "interaction_skill_catalog"); n != 0 {
		t.Fatal("preview mutated catalog", n)
	}
	w = apiRequest(s, "POST", path+"/decision", fmt.Sprintf(`{"request_id":"confirm","operation_id":"preview","digest":%q,"decision":"confirm"}`, preview.Digest))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"committed"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, route := range []string{path, path + "/operations/preview", path + "/operations/confirm", path + "/" + preview.CatalogID + "/versions/" + preview.RevisionID} {
		w = apiRequest(s, "GET", route, "")
		if w.Code != 200 {
			t.Fatal(route, w.Code, w.Body.String())
		}
	}
	w = apiRequest(s, "GET", path+"/"+preview.CatalogID, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var entry interaction.SkillCatalogEntry
	if e = json.Unmarshal(w.Body.Bytes(), &entry); e != nil {
		t.Fatal(e)
	}
	if entry.State != "disabled" || entry.SelectedRevision != "" || entry.Evaluation != "not_run" {
		t.Fatal("metadata import granted execution", entry)
	}
	for _, request := range []string{"preview", "confirm"} {
		w = apiRequest(s, "GET", "/api/v1/sessions/"+other.ID+"/skills/operations/"+request, "")
		if w.Code != 404 {
			t.Fatal("foreign operation recovery", w.Code, w.Body.String())
		}
	}
	if n := unavailableCount(t, rt, "interaction_skill_catalog"); n != 1 {
		t.Fatal("catalog count", n)
	}
	for _, table := range []string{"interaction_runs", "interaction_workspaces", "interaction_tool_proposals"} {
		if n := unavailableCount(t, rt, table); n != 0 {
			t.Fatal("stored metadata gained effects", table, n)
		}
	}
	// Committed metadata/audit ledger rows are intended, not forbidden effects.
}
func TestUnavailableNativeRequestsFailBeforeEffects(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	for _, mode := range []string{"default", "plan", "acceptEdits"} {
		w := apiRequest(s, "POST", "/api/v1/sessions", fmt.Sprintf(`{"request_id":%q,"mode":%q}`, "refused-"+mode, mode))
		var refused struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if w.Code != 409 {
			t.Fatalf("expected workspace setup refusal before decoding session: %d %s", w.Code, w.Body.String())
		}
		if e := json.Unmarshal(w.Body.Bytes(), &refused); e != nil {
			t.Fatal(e)
		}
		if refused.Error.Code != "workspace_unavailable_choose_chat_only" {
			t.Fatal(refused)
		}
	}
	// Durable rejected admission/audit records are allowed; domain objects are not.
	for _, table := range []string{"interaction_sessions", "interaction_api_sessions", "interaction_workspaces", "interaction_runs", "interaction_tool_proposals"} {
		if n := unavailableCount(t, rt, table); n != 0 {
			t.Fatal("refused native request published domain state", table, n)
		}
	}
	marker := filepath.Join(t.TempDir(), "mcp-marker")
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	options := s.options
	options.MCP = interaction.MCPHostConfig{Enabled: true, Servers: []interaction.MCPServerConfig{{ID: "synthetic-peer", Command: executable, Args: []string{"-test.run=^TestAPIMCPPeer$"}, Environment: []string{"PA_MCP_FIXTURE=api", "PA_MCP_MARKER=" + marker}, TimeoutMillis: 1000}}}
	if e = interaction.ValidateMCPConfig(options.MCP); e != nil {
		t.Fatal("MCP fixture not valid", e)
	}
	before := unavailableCount(t, rt, "interaction_mcp_admissions")
	rejected, e := NewInteractionService(rt, options)
	if rejected != nil {
		rejected.Close()
		t.Fatal("MCP unexpectedly admitted")
	}
	if e == nil || e.Error() != "MCP requires valid trusted configuration and explicit workspace" {
		t.Fatalf("early MCP capability refusal: %v", e)
	}
	if n := unavailableCount(t, rt, "interaction_mcp_admissions"); n != before {
		t.Fatal("MCP startup published admission", before, n)
	}
	for _, p := range []string{marker, marker + ".launch"} {
		if _, e = os.Stat(p); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("MCP startup side effect", p, e)
		}
	}
	for _, table := range []string{"interaction_sessions", "interaction_api_sessions", "interaction_workspaces", "interaction_runs", "interaction_tool_proposals"} {
		if n := unavailableCount(t, rt, table); n != 0 {
			t.Fatal("MCP refusal published domain state", table, n)
		}
	}
	// New explicit request; never silently reinterpret a rejected workspace claim.
	chat := unavailableChatSession(t, s, "explicit-recovery-chat")
	unavailableChatRound(t, s, chat.ID)
	if n := unavailableCount(t, rt, "interaction_workspaces"); n != 0 {
		t.Fatal("chat recovered native authority", n)
	}
}
