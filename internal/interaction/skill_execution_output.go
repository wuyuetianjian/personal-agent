package interaction

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type skillOutputSink struct {
	store *Store
	runID string
}

func (s skillOutputSink) reserve(ctx context.Context, tx *sql.Tx, proposal, slot string, payload []byte) error {
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT prepared FROM interaction_tool_proposals WHERE id=? AND run_id=?`, proposal, s.runID).Scan(&raw); err != nil {
		return ErrConflict
	}
	var p preparedTool
	if json.Unmarshal([]byte(raw), &p) != nil || p.RunID != s.runID || p.Proposal.ID != proposal {
		return ErrConflict
	}
	_, plan, _, err := skillRunData(ctx, tx, s.runID)
	if err != nil {
		return err
	}
	i := nodeIndex(plan, p.Proposal.Call.ID)
	if i < 0 || operatorHash(plan[i].Call) != operatorHash(p.Proposal.Call) {
		return ErrConflict
	}
	if !skillEnum(slot, "raw_file", "search_text", "result_envelope") {
		return ErrInvalid
	}
	if slot == "result_envelope" && len(payload) > 65536 {
		return ErrBudget
	}
	var hash string
	var size int
	err = tx.QueryRowContext(ctx, `SELECT payload_hash,bytes FROM interaction_skill_output_slots WHERE run_id=? AND proposal_id=? AND slot=?`, s.runID, proposal, slot).Scan(&hash, &size)
	if err == nil {
		if hash != contentHash(payload) || size != len(payload) {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var spent int
	var terminal bool
	if err = tx.QueryRowContext(ctx, `SELECT output_bytes,terminal FROM interaction_skill_runs WHERE run_id=?`, s.runID).Scan(&spent, &terminal); err != nil {
		return err
	}
	if terminal {
		return ErrConflict
	}
	if len(payload) > 1048576-spent {
		return ErrBudget
	}
	if _, err = tx.ExecContext(ctx, `UPDATE interaction_skill_runs SET output_bytes=output_bytes+? WHERE run_id=?`, len(payload), s.runID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO interaction_skill_output_slots VALUES(?,?,?,?,?)`, s.runID, proposal, slot, contentHash(payload), len(payload))
	return err
}
func (s skillOutputSink) inline(ctx context.Context, proposal string, payload []byte) error {
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE interaction_skill_runs SET output_bytes=output_bytes WHERE run_id=?`, s.runID); err != nil {
		return err
	}
	if err = s.reserve(ctx, tx, proposal, "result_envelope", payload); err != nil {
		return err
	}
	return tx.Commit()
}
func artifactReference(data []byte) string {
	raw, _ := json.Marshal(map[string]any{"artifact_id": contentHash(data), "bytes": len(data), "trust": "untrusted tool/history data", "session_scoped": true})
	return string(raw)
}
func (s skillOutputSink) artifact(ctx context.Context, proposal, slot string, data []byte) (string, error) {
	return s.artifactEnvelope(ctx, proposal, slot, data, nil)
}

// Precompute both payload and final envelope before the atomic artifact insert.
// A failed envelope budget check therefore cannot leave an orphan artifact.
func (s skillOutputSink) artifactEnvelope(ctx context.Context, proposal, slot string, data, envelope []byte) (string, error) {
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE interaction_skill_runs SET output_bytes=output_bytes WHERE run_id=?`, s.runID); err != nil {
		return "", err
	}
	if err = s.reserve(ctx, tx, proposal, slot, data); err != nil {
		return "", err
	}
	if envelope != nil {
		if err = s.reserve(ctx, tx, proposal, "result_envelope", envelope); err != nil {
			return "", err
		}
	}
	var session string
	if err = tx.QueryRowContext(ctx, `SELECT session_id FROM interaction_skill_runs WHERE run_id=?`, s.runID).Scan(&session); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO interaction_artifacts(session_id,id,content) VALUES(?,?,?) ON CONFLICT DO NOTHING`, session, contentHash(data), data); err != nil {
		return "", err
	}
	return artifactReference(data), tx.Commit()
}
