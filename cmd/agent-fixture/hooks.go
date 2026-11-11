package main

import (
	"agent/internal/api"
	"agent/internal/interaction"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
)

const queuedHookManifest = `id: hooks.queued
version: "1"
name: Owned queued read search
description: inert
status: active
inputs: {readme: {type: object}, search: {type: object}}
requires: {capabilities: [workspace.read, workspace.search]}
permissions: {max_level: read_only}
privacy: {max_external_trust: local_private}
budget: {max_iterations: 2, max_tool_calls: 2}
verification: {min_coverage: 0, allow_conflicts: false}
workflow: {nodes: [{id: readme, capability: workspace.read}, {id: search, capability: workspace.search, depends_on: [readme]}]}
`

// This opt-in fixture uses normal session admission and Store prepare/confirm.
// Only the initial test run bypasses HTTP worker admission so it stays queued.
// It never directly mutates SQL, production scheduling, adapters or recovery.
func seedQueuedHookRun(ctx context.Context, handler http.Handler, store *interaction.Store, host, token string) (map[string]string, error) {
	req := httptest.NewRequest("POST", "http://"+host+"/api/v1/sessions", strings.NewReader(`{"request_id":"queued-hook-session","mode":"plan"}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Agent-Protocol", api.InteractionProtocolFlavor)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	var session struct {
		ID string `json:"id"`
	}
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &session) != nil || session.ID == "" {
		return nil, fmt.Errorf("queued fixture session admission: %d %s", response.Code, response.Body.String())
	}
	skill, err := store.PrepareSkill(ctx, session.ID, interaction.SkillIntent{RequestID: "queued-skill-p", Kind: "import", ManifestText: queuedHookManifest})
	if err != nil {
		return nil, err
	}
	skill, err = store.DecideSkill(ctx, session.ID, interaction.OperatorDecision{RequestID: "queued-skill-d", OperationID: skill.OperationID, Digest: skill.Digest, Decision: "confirm"})
	if err != nil {
		return nil, err
	}
	selected, err := store.PrepareSkill(ctx, session.ID, interaction.SkillIntent{RequestID: "queued-select-p", Kind: "enable", CatalogID: skill.CatalogID, RevisionID: skill.RevisionID})
	if err != nil {
		return nil, err
	}
	if _, err = store.DecideSkill(ctx, session.ID, interaction.OperatorDecision{RequestID: "queued-select-d", OperationID: selected.OperationID, Digest: selected.Digest, Decision: "confirm"}); err != nil {
		return nil, err
	}
	policy := `{"schema_version":1,"evaluator":"declarative-hooks-v1","rules":[{"id":"audit","event":"after_node","node_ids":[],"capabilities":[],"statuses":[],"effect":"audit"}]}`
	hook, err := store.PrepareHooks(ctx, session.ID, interaction.HookIntent{RequestID: "queued-hook-p", Kind: "import", PolicyText: policy})
	if err != nil {
		return nil, err
	}
	hook, err = store.DecideHooks(ctx, session.ID, interaction.OperatorDecision{RequestID: "queued-hook-d", OperationID: hook.OperationID, Digest: hook.Digest, Decision: "confirm"})
	if err != nil {
		return nil, err
	}
	enable, err := store.PrepareHooks(ctx, session.ID, interaction.HookIntent{RequestID: "queued-enable-p", Kind: "enable", RevisionID: hook.RevisionID})
	if err != nil {
		return nil, err
	}
	if _, err = store.DecideHooks(ctx, session.ID, interaction.OperatorDecision{RequestID: "queued-enable-d", OperationID: enable.OperationID, Digest: enable.Digest, Decision: "confirm"}); err != nil {
		return nil, err
	}
	preview, err := store.PrepareSkillRunWithHooks(ctx, session.ID, interaction.SkillRunIntent{RequestID: "queued-run-p", CatalogID: skill.CatalogID, RevisionID: skill.RevisionID, ExecutionProfile: interaction.SkillRunProfile, Inputs: json.RawMessage(`{"readme":{"path":"note.txt"},"search":{"paths":["note.txt"],"query":"old"}}`)})
	if err != nil {
		return nil, err
	}
	admitted, err := store.DecideSkillRunWithHooks(ctx, session.ID, interaction.OperatorDecision{RequestID: "queued-run-d", OperationID: preview.OperationID, Digest: preview.Digest, Decision: "confirm"})
	if err != nil {
		return nil, err
	}
	if admitted.V2 == nil || admitted.V2.State != "admitted" {
		return nil, fmt.Errorf("owned queued run was not admitted")
	}
	return map[string]string{"hook_session_id": session.ID, "hook_run_id": admitted.V2.RunID, "hook_revision_id": hook.RevisionID}, nil
}
