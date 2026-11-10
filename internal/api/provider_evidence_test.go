package api

import (
	"agent/internal/interaction"
	"agent/internal/model"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderEvidenceDurabilityAndIdentity(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	rt, e := OpenInteractionRuntime(ctx, parent)
	if e != nil {
		t.Fatal(e)
	}
	p := testSelection()
	p.Model.Capabilities = map[model.Capability]bool{model.CapabilityToolCalling: true}
	if got := rt.WithToolEvidence(ctx, p); got.ToolsSupported {
		t.Fatal("unknown enabled")
	}
	attempt, e := rt.BeginToolProbe(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	if e = rt.FinishToolProbe(ctx, p, attempt, model.ToolProbeResult{Verified: true, Reason: "synthetic_tool_call_verified"}); e != nil {
		t.Fatal(e)
	}
	verified := rt.WithToolEvidence(ctx, p)
	if !verified.ToolsSupported || verified.ToolEvidenceID == "" {
		t.Fatalf("missing evidence %+v", verified)
	}
	rt.Close()
	rt, e = OpenInteractionRuntime(ctx, parent)
	if e != nil {
		t.Fatal(e)
	}
	defer rt.Close()
	if got := rt.WithToolEvidence(ctx, p); !got.ToolsSupported || got.Binding() != verified.Binding() {
		t.Fatal("restart lost evidence")
	}
	rotated := p
	rotated.Model.Provider.APIKeyEnv = "ROTATED_SECRET"
	if !rt.WithToolEvidence(ctx, rotated).ToolsSupported {
		t.Fatal("key rotation invalidated evidence")
	}
	for _, change := range []func(*ProviderSelection){func(q *ProviderSelection) { q.Endpoint += "/other" }, func(q *ProviderSelection) { q.Model.Model = "other" }, func(q *ProviderSelection) { q.Model.Provider.RequirePrivacyGateway = true }, func(q *ProviderSelection) { q.Model.ContextWindow++ }, func(q *ProviderSelection) { q.Model.ProviderID = "other" }} {
		q := p
		change(&q)
		if rt.WithToolEvidence(ctx, q).ToolsSupported {
			t.Fatal("policy change reused evidence")
		}
	}
	attempt, e = rt.BeginToolProbe(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	if rt.WithToolEvidence(ctx, p).ToolsSupported {
		t.Fatal("in-progress reprobe retained authority")
	}
	if e = rt.FinishToolProbe(ctx, p, attempt, model.ToolProbeResult{Reason: "invalid_arguments"}); e != nil {
		t.Fatal(e)
	}
	if failed := rt.WithToolEvidence(ctx, p); failed.ToolsSupported || failed.ToolCapabilityReason != "invalid_arguments" {
		t.Fatalf("failed reprobe retained authority or lost safe diagnosis: %+v", failed)
	}
	attempt, _ = rt.BeginToolProbe(ctx, p)
	rt.FinishToolProbe(ctx, p, attempt, model.ToolProbeResult{Verified: true, Reason: "synthetic_tool_call_verified"})
	if rt.WithToolEvidence(ctx, p).Binding() == verified.Binding() {
		t.Fatal("reprobe silently restores old approvals")
	}
	if _, e = rt.DB.SQL.Exec(`UPDATE interaction_provider_evidence SET evidence='{"version":999}'`); e != nil {
		t.Fatal(e)
	}
	if got := rt.WithToolEvidence(ctx, p); got.ToolsSupported || !strings.Contains(got.ToolCapabilityReason, "unverified") {
		t.Fatal("corrupt evidence accepted")
	}
}

func TestToolEvidenceChangeCannotElevateSessionsAndChatRemainsUsable(t *testing.T) {
	p := testSelection()
	s, _ := serviceFixture(t, p)
	chat := createChat(t, s)
	workspace := namedSession(t, s, "workspace", "default")
	p.ToolsSupported = true
	p.ToolEvidenceID = "new-attempt"
	s.options.Resolve = func(context.Context, string) (ProviderSelection, error) { return p, nil }
	if _, e := s.resolveSession(context.Background(), workspace); e != errProviderChanged {
		t.Fatalf("old tool session elevated: %v", e)
	}
	if got, e := s.resolveSession(context.Background(), chat); e != nil || got.ToolsSupported {
		t.Fatalf("chat-only unnecessarily blocked/elevated: %+v %v", got, e)
	}
	fresh := namedSession(t, s, "fresh", "default")
	if got, e := s.resolveSession(context.Background(), fresh); e != nil || !got.ToolsSupported {
		t.Fatalf("new session not enabled: %v", e)
	}
	p.ToolEvidenceID = "reprobe-attempt"
	if _, e := s.resolveSession(context.Background(), fresh); e != errProviderChanged {
		t.Fatalf("prior approval/checkpoint binding reused: %v", e)
	}
	p.Endpoint += "/changed"
	if _, e := s.resolveSession(context.Background(), chat); e != errProviderChanged {
		t.Fatal("chat endpoint identity not pinned")
	}
}

func TestCapabilityDisplayTracksCurrentEvidenceWithoutGrantingOldAuthority(t *testing.T) {
	p := testSelection()
	s, _ := serviceFixture(t, p)
	status := apiRequest(s, "GET", "/api/v1/status", "")
	if !strings.Contains(status.Body.String(), `"tool_capability"`) || !strings.Contains(status.Body.String(), `"unverified"`) {
		t.Fatal(status.Body.String())
	}
	workspace := namedSession(t, s, "display", "default")
	p.ToolsSupported = true
	p.ToolEvidenceID = "verified-generation"
	p.ToolCapabilityReason = "synthetic_tool_call_verified"
	s.options.Resolve = func(context.Context, string) (ProviderSelection, error) { return p, nil }
	view, _, e := s.session(context.Background(), workspace)
	if e != nil || view.Tools || view.ToolCapability != "provider_changed" {
		t.Fatalf("old capability display %+v %v", view, e)
	}
	fresh := namedSession(t, s, "fresh-display", "default")
	view, _, e = s.session(context.Background(), fresh)
	if e != nil || !view.Tools || view.ToolCapability != "verified_synthetic" || view.NonSandboxExec {
		t.Fatalf("new capability display %+v %v", view, e)
	}
	p.ToolEvidenceID = "another-generation"
	view, _, e = s.session(context.Background(), fresh)
	if e != nil || view.Tools || view.ToolCapability != "provider_changed" {
		t.Fatalf("revoked capability display %+v %v", view, e)
	}
}

func TestLegacyChatBindingIgnoresToolEvidenceOnly(t *testing.T) {
	p := testSelection()
	p.ToolsSupported = true
	s, rt := serviceFixture(t, p)
	chat := "legacy-chat"
	if err := s.store.CreateSession(context.Background(), chat); err != nil {
		t.Fatal(err)
	}
	if err := s.store.BindChatOnly(context.Background(), chat, interaction.ChatOnlyPolicy{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(sessionBinding{ModelID: p.Model.ID, Fingerprint: p.Binding(), Tools: true})
	if _, err := rt.DB.SQL.Exec(`INSERT INTO interaction_api_sessions(session_id,request_id,binding,mode) VALUES(?,?,?,?)`, chat, "create-legacy", string(raw), "chat_only"); err != nil {
		t.Fatal(err)
	}
	p.ToolEvidenceID = "new-generation"
	s.options.Resolve = func(context.Context, string) (ProviderSelection, error) { return p, nil }
	if got, err := s.resolveSession(context.Background(), chat); err != nil || got.ToolsSupported {
		t.Fatalf("legacy chat blocked/elevated: %v", err)
	}
	p.Endpoint += "/changed"
	if _, err := s.resolveSession(context.Background(), chat); err != errProviderChanged {
		t.Fatal("legacy destination changed")
	}
}

func TestReprobeRefusesPersistedApprovalAndResumeAfterRestart(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	workspace := t.TempDir()
	path := filepath.Join(workspace, "note.txt")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	p := reviewProvider(nil, nil)
	p.ToolEvidenceID = "first-evidence"
	options := InteractionOptions{Token: "secret-token", Host: "127.0.0.1:7319", Workspace: workspace, DefaultModel: "test", Resolve: func(context.Context, string) (ProviderSelection, error) { return p, nil }}
	rt, err := OpenInteractionRuntime(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewInteractionService(rt, options)
	if err != nil {
		rt.Close()
		t.Fatal(err)
	}
	if !s.broker.Capabilities().Files.Enabled {
		s.Close()
		rt.Close()
		t.Skip("native file adapter unavailable")
	}
	session := namedSession(t, s, "old-approval", "default")
	run := postReviewMessage(t, s, session, "edit", "edit")
	snap := awaitState(t, s, session, "waiting_approval")
	approval := snap.Runs[0].Approval
	s.Close()
	rt.Close()
	p.ToolEvidenceID = "second-evidence"
	rt, err = OpenInteractionRuntime(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	s, err = NewInteractionService(rt, options)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, req := range []struct{ path, body string }{{"/api/v1/approvals/" + approval.ID + "/decision", fmt.Sprintf(`{"request_id":"approve-old","decision":"approved","digest":%q}`, approval.Digest)}, {"/api/v1/runs/" + run + "/resume", `{"request_id":"resume-old"}`}} {
		w := apiRequest(s, "POST", req.path, req.body)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "provider_changed") {
			t.Fatalf("old authority accepted: %d %s", w.Code, w.Body.String())
		}
	}
	a, err := s.store.GetApproval(ctx, approval.ID)
	if err != nil || string(a.Decision) != "pending" {
		t.Fatalf("old approval mutated: %+v %v", a, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "old" {
		t.Fatal("old approved edit executed")
	}
	w := apiRequest(s, "POST", "/api/v1/runs/"+run+"/cancel", `{"request_id":"cancel-old"}`)
	if w.Code != 202 {
		t.Fatal("cannot cancel old run", w.Body.String())
	}
	awaitState(t, s, session, "cancelled")
}

func TestEvidenceStaleFinalizationAndInterruptedReprobeFailClosed(t *testing.T) {
	ctx := context.Background()
	rt, err := OpenInteractionRuntime(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	p := testSelection()
	p.Model.Capabilities = map[model.Capability]bool{model.CapabilityToolCalling: true}
	first, err := rt.BeginToolProbe(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	second, err := rt.BeginToolProbe(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	success := model.ToolProbeResult{Verified: true, Reason: "synthetic_tool_call_verified"}
	if rt.FinishToolProbe(ctx, p, first, success) == nil || rt.WithToolEvidence(ctx, p).ToolsSupported {
		t.Fatal("stale generation overwrote latest")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if rt.FinishToolProbe(cancelled, p, second, success) == nil || rt.WithToolEvidence(ctx, p).ToolsSupported {
		t.Fatal("failed finalization enabled tools")
	}
	if err = rt.FinishToolProbe(ctx, p, second, success); err != nil {
		t.Fatal(err)
	}
	if err = rt.FinishToolProbe(ctx, p, second, success); err == nil {
		t.Fatal("completed generation rewritten")
	}
}

func TestMalformedEvidenceEnvelopeFailsClosed(t *testing.T) {
	ctx := context.Background()
	rt, err := OpenInteractionRuntime(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	p := testSelection()
	p.Model.Capabilities = map[model.Capability]bool{model.CapabilityToolCalling: true}
	id, _ := rt.BeginToolProbe(ctx, p)
	if err = rt.FinishToolProbe(ctx, p, id, model.ToolProbeResult{Verified: true, Reason: "synthetic_tool_call_verified"}); err != nil {
		t.Fatal(err)
	}
	var valid string
	if err = rt.DB.SQL.QueryRow(`SELECT evidence FROM interaction_provider_evidence`).Scan(&valid); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{`, strings.Replace(valid, `"version":1`, `"version":2`, 1), strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1), strings.TrimSuffix(valid, "}") + `,"unknown":true}`} {
		if _, err = rt.DB.SQL.Exec(`UPDATE interaction_provider_evidence SET evidence=?`, raw); err != nil {
			t.Fatal(err)
		}
		if rt.WithToolEvidence(ctx, p).ToolsSupported {
			t.Fatal("malformed/ambiguous versioned evidence enabled tools", raw)
		}
	}
}

func TestEvidenceRetainsOnlySafeFailureDiagnostics(t *testing.T) {
	ctx := context.Background()
	rt, err := OpenInteractionRuntime(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	p := testSelection()
	p.Model.Capabilities = map[model.Capability]bool{model.CapabilityToolCalling: true}
	for _, reason := range []string{"provider_auto_tool_choice_rejected", "provider_message_order_rejected", "invalid_arguments", "private arbitrary diagnostic"} {
		id, err := rt.BeginToolProbe(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		if err = rt.FinishToolProbe(ctx, p, id, model.ToolProbeResult{Reason: reason}); err != nil {
			t.Fatal(err)
		}
		got := rt.WithToolEvidence(ctx, p)
		want := reason
		if strings.Contains(reason, "private") {
			want = "tool_capability_unverified"
		}
		if got.ToolsSupported || got.ToolCapabilityReason != want {
			t.Fatalf("unsafe/incorrect diagnostic %+v", got)
		}
		var raw string
		rt.DB.SQL.QueryRow(`SELECT evidence FROM interaction_provider_evidence`).Scan(&raw)
		if strings.Contains(raw, "private arbitrary") {
			t.Fatal("raw diagnostic stored")
		}
	}
}
