package api

import (
	"agent/internal/model"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

const toolEvidenceVersion = 1

// Evidence lives only in the locked, private rebuilt runtime, never legacy config.
// It contains no credentials, endpoint text, messages, arguments or raw errors.
type toolEvidence struct {
	Version   int    `json:"version"`
	Identity  string `json:"identity"`
	Attempt   string `json:"attempt"`
	CheckedAt string `json:"checked_at"`
	Verified  bool   `json:"verified"`
	Reason    string `json:"reason"`
}

func (p ProviderSelection) evidenceIdentity() string {
	p.ToolsSupported = false
	p.ToolEvidenceID = ""
	return p.Binding()
}
func (r *InteractionRuntime) WithToolEvidence(ctx context.Context, p ProviderSelection) ProviderSelection {
	p.ToolsSupported = false
	p.ToolEvidenceID = ""
	p.ToolCapabilityReason = "tool_capability_unverified"
	if !p.Model.Capabilities[model.CapabilityToolCalling] {
		p.ToolCapabilityReason = "tool_calling_disabled_by_config"
		return p
	}
	var raw string
	if err := r.DB.SQL.QueryRowContext(ctx, `SELECT evidence FROM interaction_provider_evidence WHERE model_id=? AND length(evidence)<=4096`, p.Model.ID).Scan(&raw); err != nil {
		return p
	}
	var e toolEvidence
	if !decodeToolEvidence(raw, &e) || e.Identity != p.evidenceIdentity() {
		return p
	}
	if _, err := uuid.Parse(e.Attempt); err != nil {
		return p
	}
	if _, err := time.Parse(time.RFC3339Nano, e.CheckedAt); err != nil {
		return p
	}
	p.ToolCapabilityReason = safeToolEvidenceReason(e.Reason)
	if e.Verified && e.Reason == "synthetic_tool_call_verified" {
		p.ToolsSupported = true
		p.ToolEvidenceID = e.Attempt
		p.ToolCapabilityReason = e.Reason
	}
	return p
}

// BeginToolProbe atomically revokes old evidence BEFORE network activity. A
// crash, cancellation, failed final write or failed reprobe leaves it unverified.
func (r *InteractionRuntime) BeginToolProbe(ctx context.Context, p ProviderSelection) (string, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	e := toolEvidence{Version: toolEvidenceVersion, Identity: p.evidenceIdentity(), Attempt: uuid.NewString(), CheckedAt: time.Now().UTC().Format(time.RFC3339Nano), Reason: "probe_in_progress"}
	raw, _ := json.Marshal(e)
	_, err := r.DB.SQL.ExecContext(ctx, `INSERT INTO interaction_provider_evidence(model_id,evidence) VALUES(?,?) ON CONFLICT(model_id) DO UPDATE SET evidence=excluded.evidence`, p.Model.ID, string(raw))
	return e.Attempt, err
}
func (r *InteractionRuntime) FinishToolProbe(ctx context.Context, p ProviderSelection, id string, result model.ToolProbeResult) error {
	// Only a matching in-progress generation can be finalized; stale attempts
	// never overwrite a newer attempt. Store a safe fixed reason, not provider text.
	if _, err := uuid.Parse(id); err != nil {
		return err
	}
	verified := result.Verified && result.Reason == "synthetic_tool_call_verified" && p.Model.Capabilities[model.CapabilityToolCalling]
	reason := safeToolEvidenceReason(result.Reason)
	if verified {
		reason = "synthetic_tool_call_verified"
	}
	e := toolEvidence{Version: toolEvidenceVersion, Identity: p.evidenceIdentity(), Attempt: id, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano), Verified: verified, Reason: reason}
	raw, _ := json.Marshal(e)
	tx, err := r.DB.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldRaw string
	var old toolEvidence
	if err = tx.QueryRowContext(ctx, `SELECT evidence FROM interaction_provider_evidence WHERE model_id=? AND length(evidence)<=4096`, p.Model.ID).Scan(&oldRaw); err != nil {
		return err
	}
	if !decodeToolEvidence(oldRaw, &old) || old.Attempt != id || old.Identity != e.Identity || old.Reason != "probe_in_progress" {
		return errors.New("probe generation changed")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE interaction_provider_evidence SET evidence=? WHERE model_id=?`, string(raw), p.Model.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// This is an internal canonical format, not a user-editable config. Reject
// duplicate/unknown fields and ambiguous or incompatible records fail-closed.
func decodeToolEvidence(raw string, evidence *toolEvidence) bool {
	if len(raw) > 4096 || json.Unmarshal([]byte(raw), evidence) != nil || evidence.Version != toolEvidenceVersion {
		return false
	}
	canonical, err := json.Marshal(evidence)
	return err == nil && string(canonical) == raw
}

func safeToolEvidenceReason(reason string) string {
	switch reason {
	case "probe_in_progress", "nonce_unavailable", "timeout_or_cancelled", "invalid_or_incomplete_stream", "expected_tool_call_missing", "invalid_tool_call", "invalid_arguments", "provider_auto_tool_choice_rejected", "provider_message_order_rejected", "provider_http_rejected":
		return reason
	default:
		return "tool_capability_unverified"
	}
}
