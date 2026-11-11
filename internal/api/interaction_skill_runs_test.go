package api

import (
	"agent/internal/interaction"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const executionManifest = `id: public.read
version: "1"
name: Read and search
description: Local deterministic fixture
status: active
inputs: {readme: {type: object}, search: {type: object}}
requires: {capabilities: [workspace.read, workspace.search]}
permissions: {max_level: read_only}
privacy: {max_external_trust: local_private}
budget: {max_iterations: 2, max_tool_calls: 2}
verification: {min_coverage: 0, allow_conflicts: false}
workflow: {nodes: [{id: readme, capability: workspace.read}, {id: search, capability: workspace.search, depends_on: [readme]}]}
`

func seedPublicRunSkill(t *testing.T, s *InteractionService, session string) (string, string) {
	t.Helper()
	p, e := s.store.PrepareSkill(t.Context(), session, interaction.SkillIntent{RequestID: "catalog-p", Kind: "import", ManifestText: executionManifest})
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.store.DecideSkill(t.Context(), session, interaction.OperatorDecision{RequestID: "catalog-d", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
	if e != nil {
		t.Fatal(e)
	}
	p, e = s.store.PrepareSkill(t.Context(), session, interaction.SkillIntent{RequestID: "select-p", Kind: "enable", CatalogID: d.CatalogID, RevisionID: d.RevisionID})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.store.DecideSkill(t.Context(), session, interaction.OperatorDecision{RequestID: "select-d", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
	if e != nil {
		t.Fatal(e)
	}
	return d.CatalogID, d.RevisionID
}
func previewPublicRun(t *testing.T, s *InteractionService, session, catalog, revision, request, inputs string) interaction.SkillRunOperationV2 {
	t.Helper()
	raw := fmt.Sprintf(`{"request_id":%q,"catalog_id":%q,"revision_id":%q,"execution_profile":"readonly-workspace-v1","inputs":%s}`, request, catalog, revision, inputs)
	w := apiRequest(s, "POST", "/api/v1/sessions/"+session+"/skill-runs/preview", raw)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var p interaction.SkillRunOperationV2
	if e := json.Unmarshal(w.Body.Bytes(), &p); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestPublicSkillRunLifecycleOfflineSingleDispatch(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	if !s.broker.Capabilities().Files.Enabled {
		t.Skip("native unavailable")
	}
	session := namedSession(t, s, "run-public", "plan")
	cat, rev := seedPublicRunSkill(t, s, session)
	os.WriteFile(filepath.Join(s.options.Workspace, "README.md"), []byte("public-owned-evidence"), 0600)
	s.options.Resolve = func(context.Context, string) (ProviderSelection, error) {
		t.Error("provider resolved")
		return ProviderSelection{}, fmt.Errorf("offline")
	}
	p := previewPublicRun(t, s, session, cat, rev, "public-p", `{"readme":{"path":"README.md"},"search":{"paths":["README.md"],"query":"public-owned"}}`)
	if p.Review.Eligibility != "ready" {
		t.Fatal(p)
	}
	var count int
	rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_runs`).Scan(&count)
	if count != 0 {
		t.Fatal("preview executed", count)
	}
	path := "/api/v1/sessions/" + session + "/skill-runs"
	raw := fmt.Sprintf(`{"request_id":"public-d","operation_id":"public-p","digest":%q,"decision":"confirm"}`, p.Digest)
	w := apiRequest(s, "POST", path+"/decision", raw)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var d interaction.SkillRunOperationV2
	json.Unmarshal(w.Body.Bytes(), &d)
	if d.State != "admitted" {
		t.Fatal(d)
	}
	deadline := time.Now().Add(5 * time.Second)
	var result interaction.SkillRunRecordV2
	for time.Now().Before(deadline) {
		w = apiRequest(s, "GET", path+"/"+d.RunID, "")
		json.Unmarshal(w.Body.Bytes(), &result)
		if result.Execution == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if result.Execution != "completed" || len(result.Nodes) != 2 || result.Nodes[0].Outcome != "success" || result.Nodes[1].Outcome != "success" || result.Evaluation != "not_run" {
		t.Fatal(w.Code, w.Body.String())
	}
	// Lost acknowledgment reads the original decision; repeated explicit same-ID HTTP replay cannot dispatch twice.
	for i := 0; i < 3; i++ {
		w = apiRequest(s, "GET", path+"/operations/public-d", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), d.RunID) {
			t.Fatal(w.Code, w.Body.String())
		}
		w = apiRequest(s, "POST", path+"/decision", raw)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_runs`).Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
	rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_api_actions`).Scan(&count)
	if count != 1 {
		t.Fatal("new generic acknowledgment created", count)
	} // only session admission
	w = apiRequest(s, "GET", path+"?limit=1", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), d.RunID) {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestPublicSkillRunExactGuardsAndEscapedInput(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	session := namedSession(t, s, "guards", "plan")
	cat, rev := seedPublicRunSkill(t, s, session)
	path := "/api/v1/sessions/" + session + "/skill-runs"
	for _, suffix := range []string{"?limit=0", "?limit=01", "?limit=101", "?limit=1&limit=2", "?x=1", "/operations/a?x=1", "/preview/", "/%61"} {
		w := apiRequest(s, "GET", path+suffix, "")
		if w.Code == 200 {
			t.Fatal(suffix, w.Body.String())
		}
	}
	for _, input := range []string{`{}`, `{"request_id":"x","request_id":"x"}`, `{"request_id":"x","catalog_id":"x","revision_id":"y","execution_profile":"readonly-workspace-v1","inputs":null}`, `{"request_id":"..","catalog_id":"x","revision_id":"y","execution_profile":"readonly-workspace-v1","inputs":{}}`} {
		w := apiRequest(s, "POST", path+"/preview", input)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	// Exactly 32768 canonical input bytes, valid regardless of JSON escape spelling.
	prefix := `{"readme":{"path":"README.md"},"search":{"paths":["README.md"],"query":"`
	suffix := `"}}`
	canonical := prefix + strings.Repeat("a", 32768-len(prefix)-len(suffix)) + suffix
	raw := fmt.Sprintf(`{"request_id":"escaped","catalog_id":%q,"revision_id":%q,"execution_profile":"readonly-workspace-v1","inputs":%s}`, cat, rev, strings.ReplaceAll(canonical, "a", `\u0061`))
	w := apiRequest(s, "POST", path+"/preview", raw)
	if w.Code != 200 {
		t.Fatal("maximum escaped envelope rejected", w.Code, w.Body.String())
	}
	// Common auth/Host/Origin/protocol/content-type gates still run before the dedicated route.
	for _, check := range []struct {
		header, value string
		status        int
	}{{"Authorization", "", 401}, {"Origin", "https://evil.invalid", 403}, {"X-Agent-Protocol", "old", 426}, {"Content-Type", "text/plain", 415}} {
		r := httptest.NewRequest("POST", path+"/preview", strings.NewReader(raw))
		r.Host = s.options.Host
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Authorization", "Bearer "+s.options.Token)
		r.Header.Set("X-Agent-Protocol", InteractionProtocolFlavor)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(check.header, check.value)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != check.status {
			t.Fatal(check, w.Code, w.Body.String())
		}
	}
}
func TestPublicSkillRunCapacityBeforeAdmission(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	session := namedSession(t, s, "capacity", "plan")
	cat, rev := seedPublicRunSkill(t, s, session)
	p := previewPublicRun(t, s, session, cat, rev, "capacity-p", `{"readme":{"path":"README.md"},"search":{"paths":["README.md"],"query":"x"}}`)
	s.mu.Lock()
	for i := 0; i < maxInteractionExecutors; i++ {
		s.workers[fmt.Sprint(i)] = &execution{}
	}
	s.mu.Unlock()
	w := apiRequest(s, "POST", "/api/v1/sessions/"+session+"/skill-runs/decision", fmt.Sprintf(`{"request_id":"capacity-d","operation_id":%q,"digest":%q,"decision":"confirm"}`, p.OperationID, p.Digest))
	s.mu.Lock()
	s.workers = map[string]*execution{}
	s.mu.Unlock()
	if w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, e := s.store.SkillRunOperation(t.Context(), session, "capacity-d"); e == nil {
		t.Fatal("capacity refusal admitted")
	}
}
func TestPublicSkillControlOriginalReceiptOnly(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	if !s.broker.Capabilities().Files.Enabled {
		t.Skip("native unavailable")
	}
	session := namedSession(t, s, "receipt", "plan")
	run := admitAPISkill(t, s, session)
	w := apiRequest(s, "POST", "/api/v1/runs/"+run+"/cancel", `{"request_id":"receipt-cancel"}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	path := "/api/v1/sessions/" + session + "/skill-runs/controls/" + run + "/cancel/receipt-cancel"
	w = apiRequest(s, "GET", path, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"phase":"cancelling"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, bad := range []string{strings.Replace(path, "/cancel/", "/resume/", 1), strings.Replace(path, run, "foreign", 1), strings.Replace(path, session, "foreign", 1)} {
		if apiRequest(s, "GET", bad, "").Code == 200 {
			t.Fatal("foreign receipt accepted", bad)
		}
	}
	rt.DB.SQL.Exec(`DELETE FROM interaction_api_actions WHERE request_id='receipt-cancel'`)
	for i := 0; i < 3; i++ {
		w = apiRequest(s, "GET", path, "")
		if w.Code != 409 || !strings.Contains(w.Body.String(), "needs_reconciliation") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var n int
	rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_api_actions WHERE request_id='receipt-cancel'`).Scan(&n)
	if n != 0 {
		t.Fatal("GET recreated acknowledgment")
	}
}
func TestPublicSkillRunEnvelopeBounds(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	session := namedSession(t, s, "envelope", "plan")
	cat, rev := seedPublicRunSkill(t, s, session)
	path := "/api/v1/sessions/" + session + "/skill-runs/preview"
	for _, body := range []string{fmt.Sprintf(`{"request_id":"over","catalog_id":%q,"revision_id":%q,"execution_profile":"readonly-workspace-v1","inputs":{"x":%q}}`, cat, rev, strings.Repeat("a", 32761)), fmt.Sprintf(`{"request_id":%q,"catalog_id":%q,"revision_id":%q,"execution_profile":"readonly-workspace-v1","inputs":{}}`, strings.Repeat("a", 129), cat, rev), strings.Repeat(" ", 6*32768+4097)} {
		w := apiRequest(s, "POST", path, body)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
func TestPublicSkillRunPartialAndStaleSelection(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	if !s.broker.Capabilities().Files.Enabled {
		t.Skip("native unavailable")
	}
	session := namedSession(t, s, "partial", "plan")
	cat, rev := seedPublicRunSkill(t, s, session)
	path := "/api/v1/sessions/" + session + "/skill-runs"
	p := previewPublicRun(t, s, session, cat, rev, "partial-p", `{"readme":{"path":"missing.txt"},"search":{"paths":["missing.txt"],"query":"x"}}`)
	w := apiRequest(s, "POST", path+"/decision", fmt.Sprintf(`{"request_id":"partial-d","operation_id":%q,"digest":%q,"decision":"confirm"}`, p.OperationID, p.Digest))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var d interaction.SkillRunOperationV2
	json.Unmarshal(w.Body.Bytes(), &d)
	deadline := time.Now().Add(5 * time.Second)
	var run interaction.SkillRunRecordV2
	for time.Now().Before(deadline) {
		view, _ := s.store.SkillRunWithHooks(t.Context(), session, d.RunID)
		if view.V2 != nil {
			run = *view.V2
		}
		if run.Execution == "failed" {
			break
		}
		time.Sleep(time.Millisecond * 10)
	}
	if run.Execution != "failed" || run.Nodes[0].Outcome != "failed" || run.Nodes[1].Outcome != "not_started" {
		t.Fatal(run)
	}
	p = previewPublicRun(t, s, session, cat, rev, "stale-p", `{"readme":{"path":"missing.txt"},"search":{"paths":["missing.txt"],"query":"x"}}`)
	q, e := s.store.PrepareSkill(t.Context(), session, interaction.SkillIntent{RequestID: "disable-p", Kind: "disable", CatalogID: cat})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.store.DecideSkill(t.Context(), session, interaction.OperatorDecision{RequestID: "disable-d", OperationID: q.OperationID, Digest: q.Digest, Decision: "confirm"}); e != nil {
		t.Fatal(e)
	}
	w = apiRequest(s, "POST", path+"/decision", fmt.Sprintf(`{"request_id":"stale-d","operation_id":%q,"digest":%q,"decision":"confirm"}`, p.OperationID, p.Digest))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"reason":"selection_changed"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	p = previewPublicRun(t, s, session, cat, rev, "blocked-p", `{"readme":{"path":"missing.txt"},"search":{"paths":["missing.txt"],"query":"x"}}`)
	if p.Review.Reason != "selection_unavailable" {
		t.Fatal(p)
	}
}
func TestPublicSkillRunWireGolden(t *testing.T) {
	h := strings.Repeat("a", 64)
	cat := "skill-" + h
	rev := "skill-rev-12345678-1234-4123-8123-123456789abc"
	gen := "12345678-1234-4123-8123-123456789abc"
	b := interaction.SkillRunBudgets{MaxAttempts: 1, DurationMS: 120000, MaxOrdinaryEvents: 512, MaxControlEvents: 64, MaxInputBytes: 32768, MaxResultBytes: 65536, MaxOutputBytes: 1048576}
	review := interaction.SkillRunReview{ExecutionProfile: interaction.SkillRunProfile, Eligibility: "ready", Reason: "ready", InputsHash: h, PlanHash: h, Nodes: []interaction.SkillRunNode{{NodeID: "readme", Capability: "workspace.read", DependsOn: []string{}, Call: interaction.ToolCall{ID: "readme", Name: "read", Arguments: `{"path":"note.txt","scope":"scope"}`}}}, Budgets: b, Evaluation: "not_run", VerificationStatus: "not_evaluated"}
	plan, _ := json.Marshal(review.Nodes)
	review.PlanHash = fmt.Sprintf("%x", sha256.Sum256(plan))
	review.InputsHash = fmt.Sprintf("%x", sha256.Sum256([]byte(`{"readme":{"path":"note.txt"}}`)))
	p := interaction.SkillRunOperation{SchemaVersion: 1, Namespace: "runtime-rebuild-v1", OperationID: "preview", SessionID: "session", CatalogID: cat, RevisionID: rev, ScopeID: "scope", RootIdentity: "root", PolicyDigest: h, ExpectedGeneration: gen, ExpectedRevision: 1, Review: review, ExpiresAtMS: 12345, Digest: h, State: "pending", Reason: "ready"}
	pin := interaction.SkillRunPin{SchemaVersion: 1, SessionID: "session", RunID: gen, CatalogID: cat, RevisionID: rev, CatalogGeneration: gen, ScopeID: "scope", RootIdentity: "root", PolicyDigest: h, ArtifactHash: h, ContentHash: h, ReviewHash: h, InputsHash: h, PlanHash: h, ExecutionProfile: interaction.SkillRunProfile, AdmissionTimeMS: 1000, DeadlineMS: 121000, Budgets: b}
	r := interaction.SkillRunRecord{SchemaVersion: 1, SessionID: "session", RunID: gen, Pin: pin, Execution: "completed", Evaluation: "not_run", VerificationStatus: "not_evaluated", Nodes: []interaction.SkillRunNodeResult{{NodeID: "readme", Capability: "workspace.read", ProposalID: "proposal", Outcome: "success", Result: &interaction.ToolResult{Content: "owned fixture"}}}}
	blocked := p
	blocked.Review.Eligibility = "blocked"
	blocked.Review.Reason = "selection_unavailable"
	blocked.Reason = "selection_unavailable"
	seal := func(v *interaction.SkillRunOperation) {
		v.Digest = ""
		raw, _ := json.Marshal(v)
		v.Digest = fmt.Sprintf("%x", sha256.Sum256(raw))
	}
	seal(&p)
	seal(&blocked)
	fixture := map[string]any{"pending": p, "blocked": blocked, "completed": r, "page": interaction.SkillRunPage{SchemaVersion: 1, SessionID: "session", Runs: []interaction.SkillRunRecord{r}}, "control": SkillRunControlReceipt{1, "session", "stop", gen, "cancel", "completed", "cancelling"}}
	raw, _ := json.MarshalIndent(fixture, "", "  ")
	raw = append(raw, '\n')
	path := "../../protocol/v1/skill_runs.golden.json"
	if os.Getenv("UPDATE_WIRE_FIXTURES") == "1" {
		if e := os.WriteFile(path, raw, 0644); e != nil {
			t.Fatal(e)
		}
	}
	want, e := os.ReadFile(path)
	if e != nil || string(want) != string(raw) {
		t.Fatal("skill run wire fixture drift", e)
	}
}

// Review R3: live session controls must see an ordinary valid created frame.
// This asserts the stored event and public projection, not a fabricated UI run.
func TestPublicSkillRunCreatedEventCarriesAdmittedIdentity(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	if !s.broker.Capabilities().Files.Enabled {
		t.Skip("native unavailable")
	}
	session := namedSession(t, s, "created-event", "plan")
	id := admitAPISkill(t, s, session)
	events, e := s.store.Events(t.Context(), session, 0, 100)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, event := range events {
		if event.RunID != id || event.Type != "run.created" {
			continue
		}
		found = true
		var stored interaction.Run
		if e := json.Unmarshal(event.Payload, &stored); e != nil {
			t.Fatal(e)
		}
		if stored.ID != id || stored.SessionID != session || stored.RequestID != "confirm" || stored.State != interaction.RunRunning || stored.Message.Content == "" {
			t.Fatalf("created event lost admitted identity: %s", event.Payload)
		}
		if len(event.Payload) > 128<<10 {
			t.Fatal("created payload exceeded existing event bound")
		}
		wire := projectEvent(event)
		if wire.SchemaVersion != 1 || wire.SessionID != session || wire.RunID != id {
			t.Fatal("foreign created wire identity", wire)
		}
		raw, _ := json.Marshal(wire)
		var view struct {
			Data struct {
				Request string `json:"request_id"`
				Input   string `json:"input"`
				State   string `json:"state"`
			} `json:"data"`
		}
		json.Unmarshal(raw, &view)
		if view.Data.Request != "confirm" || view.Data.Input == "" || view.Data.State != "running" {
			t.Fatalf("invalid public created frame: %s", raw)
		}
	}
	if !found {
		t.Fatal("missing created event")
	}
	var ordinary, control, retained, reserved, created, acks int
	if e := rt.DB.SQL.QueryRow(`SELECT ordinary_events,control_events,retained_bytes,reserved_bytes FROM interaction_skill_runs WHERE run_id=?`, id).Scan(&ordinary, &control, &retained, &reserved); e != nil {
		t.Fatal(e)
	}
	rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_events WHERE run_id=? AND type='run.created'`, id).Scan(&created)
	rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_api_actions WHERE request_id='confirm'`).Scan(&acks)
	if ordinary != 1 || control != 0 || created != 1 || acks != 0 || retained <= 0 || retained > reserved || reserved > 8<<20 {
		t.Fatal("created event changed bounded ledger semantics", ordinary, control, created, acks, retained, reserved)
	}
}

func TestSkillHooksWireHandoffPublicV2(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	if !s.broker.Capabilities().Files.Enabled {
		t.Skip("native unavailable")
	}
	session := namedSession(t, s, "hooks-wire", "plan")
	cat, rev := seedPublicRunSkill(t, s, session)
	p, e := s.store.PrepareSkillRunWithHooks(t.Context(), session, interaction.SkillRunIntent{RequestID: "hooks-preview", CatalogID: cat, RevisionID: rev, ExecutionProfile: interaction.SkillRunProfile, Inputs: json.RawMessage(`{"readme":{"path":"README.md"},"search":{"paths":["README.md"],"query":"owned"}}`)})
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.store.DecideSkillRunWithHooks(t.Context(), session, interaction.OperatorDecision{RequestID: "hooks-confirm", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
	if e != nil || a.V2 == nil {
		t.Fatal(a, e)
	}
	for _, suffix := range []string{a.V2.RunID, "operations/hooks-preview", "operations/hooks-confirm"} {
		w := apiRequest(s, "GET", "/api/v1/sessions/"+session+"/skill-runs/"+suffix, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"schema_version":2`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
