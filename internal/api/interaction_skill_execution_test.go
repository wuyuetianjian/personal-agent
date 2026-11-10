package api

import (
	"agent/internal/interaction"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func admitAPISkill(t *testing.T, s *InteractionService, session string) string {
	t.Helper()
	ctx := t.Context()
	raw := `id: local.read
version: "1"
name: Read
description: Inert
status: active
inputs: {readme: {type: object}}
requires: {capabilities: [workspace.read]}
permissions: {max_level: read_only}
privacy: {max_external_trust: local_private}
budget: {max_iterations: 1, max_tool_calls: 1}
verification: {min_coverage: 0, allow_conflicts: false}
workflow: {nodes: [{id: readme, capability: workspace.read}]}
`
	p, e := s.store.PrepareSkill(ctx, session, interaction.SkillIntent{RequestID: "import", Kind: "import", ManifestText: raw})
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.store.DecideSkill(ctx, session, interaction.OperatorDecision{RequestID: "import-d", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
	if e != nil {
		t.Fatal(e)
	}
	p, e = s.store.PrepareSkill(ctx, session, interaction.SkillIntent{RequestID: "enable", Kind: "enable", CatalogID: d.CatalogID, RevisionID: d.RevisionID})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.store.DecideSkill(ctx, session, interaction.OperatorDecision{RequestID: "enable-d", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"}); e != nil {
		t.Fatal(e)
	}
	run, e := s.store.PrepareSkillRun(ctx, session, interaction.SkillRunIntent{RequestID: "preview", CatalogID: d.CatalogID, RevisionID: d.RevisionID, ExecutionProfile: interaction.SkillRunProfile, Inputs: json.RawMessage(`{"readme":{"path":"README.md"}}`)})
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.store.DecideSkillRun(ctx, session, interaction.OperatorDecision{RequestID: "confirm", OperationID: run.OperationID, Digest: run.Digest, Decision: "confirm"})
	if e != nil || a.State != "admitted" {
		t.Fatal(a, e)
	}
	return a.RunID
}
func TestSkillRunAPIResumeOfflineWithoutModelTools(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	if !s.broker.Capabilities().Files.Enabled {
		t.Skip("native files unavailable")
	}
	session := namedSession(t, s, "skill", "plan")
	id := admitAPISkill(t, s, session)
	os.WriteFile(filepath.Join(s.options.Workspace, "README.md"), []byte("hello"), 0600)
	s.options.Resolve = func(context.Context, string) (ProviderSelection, error) {
		return ProviderSelection{}, errors.New("offline; must not resolve")
	}
	got := apiRequest(s, "POST", "/api/v1/runs/"+id+"/resume", `{"request_id":"resume-skill"}`)
	if got.Code != 202 {
		t.Fatal(got.Code, got.Body.String())
	}
	awaitState(t, s, session, "completed")
}
func TestSkillRunAPIStartupAndCancelStayOffline(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	if !s.broker.Capabilities().Files.Enabled {
		t.Skip("native files unavailable")
	}
	session := namedSession(t, s, "skill-cancel", "plan")
	id := admitAPISkill(t, s, session)
	opts := s.options
	opts.Resolve = func(context.Context, string) (ProviderSelection, error) {
		t.Error("provider resolver used for deterministic lifecycle")
		return ProviderSelection{}, errors.New("offline")
	}
	s.Close()
	restarted, e := NewInteractionService(rt, opts)
	if e != nil {
		t.Fatal(e)
	}
	defer restarted.Close()
	got := apiRequest(restarted, "POST", "/api/v1/runs/"+id+"/cancel", `{"request_id":"cancel-skill"}`)
	if got.Code != 202 {
		t.Fatal(got.Code, got.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := restarted.store.GetRun(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if r.State == interaction.RunCancelled {
			return
		}
		time.Sleep(time.Millisecond * 10)
	}
	t.Fatal("cancel did not terminalize")
}
func TestPersistedRunRoutingDoesNotUseQueuePreflightFallback(t *testing.T) {
	s, _ := serviceFixture(t, testSelection())
	if !s.broker.Capabilities().Files.Enabled {
		t.Skip("native files unavailable")
	}
	session := namedSession(t, s, "missing-run", "plan")
	if _, e := s.engine(t.Context(), interaction.Run{ID: "missing", SessionID: session}, testSelection()); e == nil {
		t.Fatal("missing persisted run accepted as preflight")
	}
}
func TestSkillTerminalCancelRequestsShareBoundedRecoveryPool(t *testing.T) {
	s, rt := serviceFixture(t, testSelection())
	if !s.broker.Capabilities().Files.Enabled {
		t.Skip("native files unavailable")
	}
	session := namedSession(t, s, "bounded-control", "plan")
	id := admitAPISkill(t, s, session)
	got := apiRequest(s, "POST", "/api/v1/runs/"+id+"/cancel", `{"request_id":"cancel-once"}`)
	if got.Code != 202 {
		t.Fatal(got.Code, got.Body.String())
	}
	awaitState(t, s, session, "cancelled")
	var reservedBefore int
	rt.DB.SQL.QueryRow(`SELECT reserved_bytes FROM interaction_skill_runs WHERE run_id=?`, id).Scan(&reservedBefore)
	for i := 0; i < 20; i++ {
		got = apiRequest(s, "POST", "/api/v1/runs/"+id+"/cancel", `{"request_id":"cancel-once"}`)
		if got.Code != 202 {
			t.Fatal(got.Code, got.Body.String())
		}
	}
	var count int
	rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_skill_controls`).Scan(&count)
	if count != 1 {
		t.Fatal("same request retained duplicates", count)
	}
	tx, e := rt.DB.SQL.BeginTx(t.Context(), nil)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2045; i++ {
		if _, e = tx.Exec(`INSERT INTO interaction_skill_controls VALUES(?,?,?,4096)`, fmt.Sprintf("seed-control-%d", i), id, strings.Repeat("a", 64)); e != nil {
			tx.Rollback()
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	got = apiRequest(s, "POST", "/api/v1/runs/"+id+"/cancel", `{"request_id":"over-cap"}`)
	if got.Code != 409 {
		t.Fatal(got.Code, got.Body.String())
	}
	var reservedAfter int
	rt.DB.SQL.QueryRow(`SELECT reserved_bytes FROM interaction_skill_runs WHERE run_id=?`, id).Scan(&reservedAfter)
	if reservedAfter != reservedBefore {
		t.Fatal("terminal execution reservation reopened")
	}
	// Lost acknowledgments cannot turn the reservation into permission to replay.
	rt.DB.SQL.Exec(`DELETE FROM interaction_api_actions WHERE request_id='cancel-once'`)
	got = apiRequest(s, "POST", "/api/v1/runs/"+id+"/cancel", `{"request_id":"cancel-once"}`)
	if got.Code != 500 {
		t.Fatal(got.Code, got.Body.String())
	}
}
