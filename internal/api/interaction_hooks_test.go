package api

import (
	"agent/internal/interaction"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const hookPolicyFixture = `{"schema_version":1,"evaluator":"declarative-hooks-v1","rules":[{"id":"deny-search","event":"before_node","node_ids":[],"capabilities":["workspace.search"],"statuses":[],"effect":"deny"}]}`

func hookHTTPPrepare(t *testing.T, s *InteractionService, session, request, kind, revision, policy string) interaction.HookOperation {
	t.Helper()
	b, _ := json.Marshal(interaction.HookIntent{RequestID: request, Kind: kind, RevisionID: revision, PolicyText: policy})
	w := apiRequest(s, "POST", "/api/v1/sessions/"+session+"/hooks/preview", string(b))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var p interaction.HookOperation
	if e := json.Unmarshal(w.Body.Bytes(), &p); e != nil {
		t.Fatal(e)
	}
	return p
}
func hookHTTPDecide(t *testing.T, s *InteractionService, p interaction.HookOperation, request, decision string) interaction.HookOperation {
	t.Helper()
	b, _ := json.Marshal(interaction.OperatorDecision{RequestID: request, OperationID: p.OperationID, Digest: p.Digest, Decision: decision})
	w := apiRequest(s, "POST", "/api/v1/sessions/"+p.SessionID+"/hooks/decision", string(b))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var d interaction.HookOperation
	json.Unmarshal(w.Body.Bytes(), &d)
	return d
}
func TestHooksHTTPBoundary(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	session := namedSession(t, s, "hook-boundary", "plan")
	base := "/api/v1/sessions/" + session + "/hooks"
	s.options.Resolve = func(context.Context, string) (ProviderSelection, error) {
		t.Error("policy management resolved provider")
		return ProviderSelection{}, fmt.Errorf("offline")
	}
	w := apiRequest(s, "GET", base, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"generation":0`) {
		t.Fatal(w.Code, w.Body.String())
	}
	var n int
	rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_hook_sessions`).Scan(&n)
	if n != 0 {
		t.Fatal("read created selection")
	}
	for _, suffix := range []string{"/", "/%72evisions", "?x=1", "/revisions?limit=0", "/revisions?limit=33", "/revisions?limit=01", "/revisions?limit=1&limit=2", "/revisions?x=1", "/revisions?cursor=bad", "/revisions/../x", "/operations/no?x=1"} {
		if v := apiRequest(s, "GET", base+suffix, ""); v.Code == 200 {
			t.Fatal(suffix, v.Body.String())
		}
	}
	for _, body := range []string{`{}`, `{"request_id":"a","kind":"disable","revision_id":"","policy_text":"","extra":0}`, `{"request_id":"a","kind":"disable","revision_id":"","policy_text":null}`, `{"request_id":"a","kind":"disable","kind":"disable","revision_id":"","policy_text":""}`, strings.Repeat(" ", 53249)} {
		if v := apiRequest(s, "POST", base+"/preview", body); v.Code != 400 {
			t.Fatal(v.Code, v.Body.String())
		}
	}
	if v := apiRequest(s, "POST", base+"/decision", strings.Repeat(" ", 4097)); v.Code != 400 {
		t.Fatal(v.Code, v.Body.String())
	}
	raw, _ := json.Marshal(interaction.HookIntent{RequestID: "import", Kind: "import", PolicyText: hookPolicyFixture})
	for _, check := range []struct {
		header, value string
		status        int
	}{{"Authorization", "", 401}, {"Origin", "https://evil.invalid", 403}, {"X-Agent-Protocol", "old", 426}, {"Content-Type", "text/plain", 415}, {"Host", "evil.invalid", 403}} {
		r := httptest.NewRequest("POST", base+"/preview", strings.NewReader(string(raw)))
		r.Host = s.options.Host
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Authorization", "Bearer "+s.options.Token)
		r.Header.Set("X-Agent-Protocol", InteractionProtocolFlavor)
		r.Header.Set("Content-Type", "application/json")
		if check.header == "Host" {
			r.Host = check.value
		} else {
			r.Header.Set(check.header, check.value)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != check.status {
			t.Fatal(check, w.Code, w.Body.String())
		}
	}
	if v := apiRequest(s, "DELETE", base, ""); v.Code != 405 {
		t.Fatal(v.Code)
	}
	// The decoded policy can occupy exactly 8192 bytes while the wire uses maximal ASCII escaping.
	policy := hookPolicyFixture + strings.Repeat(" ", 8192-len(hookPolicyFixture))
	escaped := ""
	for _, c := range policy {
		escaped += fmt.Sprintf(`\u%04x`, c)
	}
	body := `{"request_id":"escaped","kind":"import","revision_id":"","policy_text":"` + escaped + `"}`
	if v := apiRequest(s, "POST", base+"/preview", body); v.Code != 200 {
		t.Fatal("escaped maximum", len(body), v.Code, v.Body.String())
	}
	bad, _ := json.Marshal(interaction.HookIntent{RequestID: "over", Kind: "import", PolicyText: policy + " "})
	if v := apiRequest(s, "POST", base+"/preview", string(bad)); v.Code != 400 {
		t.Fatal(v.Code)
	}
	rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_runs`).Scan(&n)
	if n != 0 {
		t.Fatal("management created run")
	}
}
func TestHooksHTTPAtomicLifecycle(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	session := namedSession(t, s, "hook-cycle", "plan")
	foreign := namedSession(t, s, "hook-foreign", "plan")
	base := "/api/v1/sessions/" + session + "/hooks"
	p := hookHTTPPrepare(t, s, session, "hp", "import", "", hookPolicyFixture)
	if p.State != "pending" || p.Policy == nil {
		t.Fatal(p)
	}
	d := hookHTTPDecide(t, s, p, "hd", "confirm")
	if d.Reason != "imported" {
		t.Fatal(d)
	}
	for _, request := range []string{"hp", "hd"} {
		w := apiRequest(s, "GET", base+"/operations/"+request, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"applied"`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, suffix := range []string{"/operations/hp", "/revisions/" + p.RevisionID} {
		if w := apiRequest(s, "GET", "/api/v1/sessions/"+foreign+"/hooks"+suffix, ""); w.Code != 404 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, q := range []string{"", "?limit=1", "?limit=32"} {
		w := apiRequest(s, "GET", base+"/revisions"+q, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), p.RevisionID) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	p = hookHTTPPrepare(t, s, session, "enable", "enable", p.RevisionID, "")
	b, _ := json.Marshal(interaction.OperatorDecision{RequestID: "enabled", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Go(func() { codes <- apiRequest(s, "POST", base+"/decision", string(b)).Code })
	}
	wg.Wait()
	close(codes)
	for c := range codes {
		if c != 200 {
			t.Fatal(c)
		}
	}
	sel, e := s.store.HookSelection(t.Context(), session)
	if e != nil || sel.Generation != 1 || sel.State != "enabled" {
		t.Fatal(sel, e)
	}
	// Reading lost decision evidence is read-only and the original change occurs once.
	for i := 0; i < 3; i++ {
		if w := apiRequest(s, "GET", base+"/operations/enabled", ""); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	var n int
	rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_hook_audits WHERE session_id=?`, session).Scan(&n)
	if n != 2 {
		t.Fatal(n)
	}
	noop := hookHTTPPrepare(t, s, session, "noop", "enable", sel.RevisionID, "")
	if hookHTTPDecide(t, s, noop, "noop-d", "confirm").Reason != "unchanged" {
		t.Fatal("not noop")
	}
	cancel := hookHTTPPrepare(t, s, session, "cancel", "disable", "", "")
	if hookHTTPDecide(t, s, cancel, "cancel-d", "cancel").State != "cancelled" {
		t.Fatal("not cancelled")
	}
	sel, _ = s.store.HookSelection(t.Context(), session)
	if sel.Generation != 1 || sel.State != "enabled" {
		t.Fatal(sel)
	}
	old := hookHTTPPrepare(t, s, session, "old", "disable", "", "")
	newer := hookHTTPPrepare(t, s, session, "new", "disable", "", "")
	if hookHTTPDecide(t, s, newer, "new-d", "confirm").Reason != "disabled" {
		t.Fatal("not disabled")
	}
	if hookHTTPDecide(t, s, old, "old-d", "confirm").Reason != "selection_changed" {
		t.Fatal("not stale")
	}
	rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_api_actions`).Scan(&n)
	if n != 2 {
		t.Fatal("generic ack copies", n)
	}
}

func TestHooksHTTPPublicPinsDenialAndLegacyReview(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	if !s.broker.Capabilities().Files.Enabled {
		t.Skip("native unavailable")
	}
	session := namedSession(t, s, "hook-run", "plan")
	cat, rev := seedPublicRunSkill(t, s, session)
	in := interaction.SkillRunIntent{RequestID: "legacy-p", CatalogID: cat, RevisionID: rev, ExecutionProfile: interaction.SkillRunProfile, Inputs: json.RawMessage(`{"readme":{"path":"note.txt"},"search":{"paths":["note.txt"],"query":"owned"}}`)}
	legacy, e := s.store.PrepareSkillRun(t.Context(), session, in)
	if e != nil {
		t.Fatal(e)
	}
	w := apiRequest(s, "POST", "/api/v1/sessions/"+session+"/skill-runs/decision", fmt.Sprintf(`{"request_id":"legacy-d","operation_id":%q,"digest":%q,"decision":"confirm"}`, legacy.OperationID, legacy.Digest))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "hook_review_required") {
		t.Fatal(w.Code, w.Body.String())
	}
	p := hookHTTPPrepare(t, s, session, "policy-p", "import", "", strings.Replace(hookPolicyFixture, `["workspace.search"]`, `[]`, 1))
	hookHTTPDecide(t, s, p, "policy-d", "confirm")
	p = hookHTTPPrepare(t, s, session, "selection-p", "enable", p.RevisionID, "")
	hookHTTPDecide(t, s, p, "selection-d", "confirm")
	preview := previewPublicRun(t, s, session, cat, rev, "run-p", string(in.Inputs))
	if preview.SchemaVersion != 2 || preview.Hooks.RevisionID != p.RevisionID {
		t.Fatal(preview)
	}
	w = apiRequest(s, "POST", "/api/v1/sessions/"+session+"/skill-runs/decision", fmt.Sprintf(`{"request_id":"run-d","operation_id":%q,"digest":%q,"decision":"confirm"}`, preview.OperationID, preview.Digest))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var admitted interaction.SkillRunOperationV2
	json.Unmarshal(w.Body.Bytes(), &admitted)
	deadline := time.Now().Add(5 * time.Second)
	var run interaction.SkillRunView
	for time.Now().Before(deadline) {
		run, e = s.store.SkillRunWithHooks(t.Context(), session, admitted.RunID)
		if e == nil && run.V2 != nil && run.V2.Execution == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e != nil || run.V2 == nil || run.V2.Execution != "failed" || run.V2.Nodes[0].Reason != "hook_denied" || run.V2.Nodes[0].Result != nil {
		t.Fatal(run, e)
	}
	var attempts, journals, claims int
	if e := rt.DB.SQL.QueryRow(`SELECT count(*) FROM side_effect_records WHERE workflow_id=?`, admitted.RunID).Scan(&journals); e != nil {
		t.Fatal(e)
	}
	if e := rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_tool_proposals WHERE run_id=? AND executed=1`, admitted.RunID).Scan(&claims); e != nil {
		t.Fatal(e)
	}
	checkpoint, err := s.store.EngineCheckpoint(t.Context(), admitted.RunID)
	if err != nil {
		t.Fatal(err)
	}
	attempts = checkpoint.Steps
	if journals != 0 || claims != 0 || attempts != 0 {
		t.Fatal(journals, claims, attempts)
	}
	for i := 0; i < 3; i++ {
		w = apiRequest(s, "GET", "/api/v1/sessions/"+session+"/skill-runs/"+admitted.RunID+"/hooks", "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var receipts interaction.HookReceiptList
		json.Unmarshal(w.Body.Bytes(), &receipts)
		if len(receipts.Receipts) != 2 || receipts.Receipts[0].Decision != "deny" {
			t.Fatal(receipts)
		}
	}
}
func TestHooksHTTPRefusalAndUnknown(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	session := namedSession(t, s, "hook-fault", "plan")
	base := "/api/v1/sessions/" + session + "/hooks"
	p := hookHTTPPrepare(t, s, session, "original", "disable", "", "")
	for _, body := range []string{`{"request_id":"different","operation_id":"original","digest":"bad","decision":"confirm"}`, fmt.Sprintf(`{"request_id":"original","operation_id":"original","digest":%q,"decision":"confirm"}`, p.Digest)} {
		w := apiRequest(s, "POST", base+"/decision", body)
		if w.Code != 400 && w.Code != 409 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for i := 0; i < 3; i++ {
		hookHTTPPrepare(t, s, session, fmt.Sprintf("capacity-%d", i), "disable", "", "")
	}
	b, _ := json.Marshal(interaction.HookIntent{RequestID: "over-capacity", Kind: "disable"})
	w := apiRequest(s, "POST", base+"/preview", string(b))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "budget_exhausted") {
		t.Fatal(w.Code, w.Body.String())
	}
	// The admitted operation's reserved decision remains available at preview capacity.
	if _, e := rt.DB.SQL.Exec(`CREATE TRIGGER hook_http_fault BEFORE INSERT ON interaction_hook_audits BEGIN SELECT RAISE(ABORT,'private SQL /filesystem secret'); END`); e != nil {
		t.Fatal(e)
	}
	body := fmt.Sprintf(`{"request_id":"lost-decision","operation_id":"original","digest":%q,"decision":"confirm"}`, p.Digest)
	w = apiRequest(s, "POST", base+"/decision", body)
	if w.Code != 500 || !strings.Contains(w.Body.String(), "outcome_unknown") || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = apiRequest(s, "GET", base+"/operations/lost-decision", "")
	if w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = apiRequest(s, "GET", base+"/operations/original", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"pending"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	rt.DB.SQL.Exec(`DROP TRIGGER hook_http_fault`)
	// A human may later cancel original pending review with a distinct decision; GET itself does not write.
	hookHTTPDecide(t, s, p, "cancel-original", "cancel")
}
