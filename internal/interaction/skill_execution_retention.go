package interaction

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Every skill event, including generic cancel/terminal events, passes this gate.
// The key uses progress, never an event counter, so restart cannot spend another
// allowance merely by observing the same checkpoint again.
func skillEventAdmission(ctx context.Context, tx *sql.Tx, r Run, kind string, raw []byte) (skip bool, err error) {
	var ordinary, control int
	var checkpoint string
	err = tx.QueryRowContext(ctx, `SELECT s.ordinary_events,s.control_events,coalesce(e.checkpoint,'') FROM interaction_skill_runs s LEFT JOIN interaction_engine e ON e.run_id=s.run_id WHERE s.run_id=?`, r.ID).Scan(&ordinary, &control, &checkpoint)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return
	}
	if len(raw) > 128<<10 {
		return false, ErrBudget
	}
	var cp Checkpoint
	if json.Unmarshal([]byte(checkpoint), &cp) != nil {
		return false, ErrReconciliation
	}
	key := operatorHash([]any{kind, string(raw), cp.Steps, cp.Calls, cp.Pending})
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM interaction_skill_event_keys WHERE run_id=? AND identity=?`, r.ID, key).Scan(&exists); err != nil {
		return
	}
	if exists != 0 {
		return true, nil
	}
	indispensable := skillEnum(kind, "run.completed", "run.failed", "run.cancelled", "run.cancelling", "run.needs_reconciliation", "tool.recovered")
	isControl := indispensable || skillEnum(kind, "run.recovered", "tool.recovered", "tool.uncertain")
	if isControl {
		if control >= 64 || (!indispensable && control >= 60) {
			return false, ErrBudget
		}
	} else if ordinary >= 512 {
		return false, ErrBudget
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO interaction_skill_event_keys VALUES(?,?)`, r.ID, key)
	return
}
