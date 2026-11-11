package main

import (
	"agent/internal/interaction"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
)

func hookHash(v any) string {
	b, e := json.Marshal(v)
	must(e)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func hookGolden() {
	text := `{"schema_version":1,"evaluator":"declarative-hooks-v1","rules":[{"id":"z-audit","event":"after_node","node_ids":["z","a"],"capabilities":["workspace.search","workspace.read"],"statuses":["success","failed"],"effect":"audit"},{"id":"deny-search","event":"before_node","node_ids":[],"capabilities":["workspace.search"],"statuses":["ready"],"effect":"deny"}]}`
	policy, canonical, e := interaction.ParseHookPolicy([]byte(text))
	must(e)
	disabled := interaction.HookRunSnapshot{SchemaVersion: 1, Evaluator: interaction.HookEvaluator, Policy: interaction.HookPolicy{SchemaVersion: 1, Evaluator: interaction.HookEvaluator, Rules: []interaction.HookRule{}}}
	disabled.PolicySHA256 = hookHash(disabled.Policy)
	snapshot := interaction.HookRunSnapshot{SchemaVersion: 1, Evaluator: interaction.HookEvaluator, Generation: 1, RevisionID: "hook-revision", PolicySHA256: hookHash(policy), Policy: policy}
	op := interaction.HookOperation{SchemaVersion: 1, Namespace: "runtime-rebuild-v1", OperationID: "preview", SessionID: "session", Kind: "import", RevisionID: "hook-revision", BeforeState: "disabled", Policy: &policy, PolicySHA256: hookHash(policy), ExpiresAtMS: 12345, State: "pending", Reason: "ready"}
	// Explicit struct projection is the frozen immutable management digest contract.
	sign := func(p interaction.HookOperation) interaction.HookOperation {
		p.Digest = hookHash(struct {
			SchemaVersion      int                     `json:"schema_version"`
			Namespace          string                  `json:"namespace"`
			OperationID        string                  `json:"operation_id"`
			SessionID          string                  `json:"session_id"`
			Kind               string                  `json:"kind"`
			RevisionID         string                  `json:"revision_id"`
			ExpectedGeneration int64                   `json:"expected_generation"`
			BeforeState        string                  `json:"before_state"`
			BeforeRevisionID   string                  `json:"before_revision_id"`
			Policy             *interaction.HookPolicy `json:"policy"`
			PolicySHA256       string                  `json:"policy_sha256"`
			ExpiresAtMS        int64                   `json:"expires_at_ms"`
		}{p.SchemaVersion, p.Namespace, p.OperationID, p.SessionID, p.Kind, p.RevisionID, p.ExpectedGeneration, p.BeforeState, p.BeforeRevisionID, p.Policy, p.PolicySHA256, p.ExpiresAtMS})
		return p
	}
	op = sign(op)
	disable := op
	disable.Kind = "disable"
	disable.Policy = nil
	disable.PolicySHA256 = ""
	disable.RevisionID = ""
	disable.BeforeState = "enabled"
	disable.BeforeRevisionID = "hook-revision"
	disable.ExpectedGeneration = 1
	disable = sign(disable)
	b, e := os.ReadFile("protocol/v1/skill_runs.golden.json")
	must(e)
	var old struct {
		Pending   interaction.SkillRunOperationV2 `json:"pending"`
		Completed interaction.SkillRunRecordV2    `json:"completed"`
	}
	must(json.Unmarshal(b, &old))
	old.Pending.SchemaVersion = 2
	old.Pending.Hooks = snapshot
	old.Pending.Digest = ""
	old.Pending.Digest = hookHash(old.Pending)
	old.Completed.SchemaVersion = 2
	old.Completed.Pin.SchemaVersion = 2
	old.Completed.Pin.Hooks = interaction.HookRunPin{SchemaVersion: 1, Evaluator: interaction.HookEvaluator, Generation: 1, RevisionID: snapshot.RevisionID, PolicySHA256: snapshot.PolicySHA256, SnapshotSHA256: hookHash(snapshot)}
	receipt := interaction.HookReceipt{SchemaVersion: 1, SessionID: "session", RunID: old.Completed.RunID, Event: "before_node", NodeID: "search", ProposalID: "proposal", ProposalDigest: hookHash("proposal"), SnapshotSHA256: hookHash(snapshot), RevisionID: snapshot.RevisionID, Capability: "workspace.search", Status: "ready", Decision: "deny", Matches: []interaction.HookMatch{{RuleID: "deny-search", Effect: "deny"}}, Reason: "matched"}
	receipt.ReceiptID = hookHash([]string{"declarative-hooks-v1:receipt", receipt.SessionID, receipt.RunID, receipt.SnapshotSHA256, receipt.Event, receipt.NodeID, receipt.ProposalID, receipt.ProposalDigest})
	out := map[string]any{"policy_text": text, "canonical_policy": string(canonical), "policy_sha256": hookHash(policy), "snapshot": snapshot, "snapshot_sha256": hookHash(snapshot), "disabled_snapshot": disabled, "disabled_snapshot_sha256": hookHash(disabled), "import": op, "disable": disable, "revision": interaction.HookRevision{SchemaVersion: 1, SessionID: "session", RevisionID: "hook-revision", PolicySHA256: hookHash(policy), Policy: policy}, "selection": interaction.HookSelection{SchemaVersion: 1, SessionID: "session", Generation: 1, State: "enabled", RevisionID: "hook-revision", PolicySHA256: hookHash(policy)}, "pending_v2": old.Pending, "completed_v2": old.Completed, "receipts": interaction.HookReceiptList{SchemaVersion: 1, SessionID: "session", RunID: receipt.RunID, Receipts: []interaction.HookReceipt{receipt}}}
	raw, e := json.MarshalIndent(out, "", "  ")
	must(e)
	must(os.WriteFile("protocol/v1/hooks.golden.json", append(raw, '\n'), 0644))
}
