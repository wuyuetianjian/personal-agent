package interaction

import (
	"context"
	"database/sql"
	"errors"
)

// ReserveSkillControl commits bounded recovery identity before cancellation or
// scheduling. A missing acknowledgment never authorizes replay of that action.
// The legacy API action acknowledgment is charged to this reservation, including
// requests made after a run released its unused execution reservation.
func (s *Store) ReserveSkillControl(ctx context.Context, id, request, fp string) error {
	if !operatorRequestID(request) || len(fp) != 64 {
		return ErrInvalid
	}
	var session string
	if err := s.db.QueryRowContext(ctx, `SELECT session_id FROM interaction_skill_runs WHERE run_id=?`, id).Scan(&session); err != nil {
		return err
	}
	tx, err := s.queueWrite(ctx, session)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldRun, oldFP string
	err = tx.QueryRowContext(ctx, `SELECT run_id,fingerprint FROM interaction_skill_controls WHERE request_id=?`, request).Scan(&oldRun, &oldFP)
	if err == nil {
		if oldRun != id || oldFP != fp {
			return ErrConflict
		}
		return ErrReconciliation
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var existing, count, total int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM interaction_skill_run_requests WHERE request_id=?`, request).Scan(&existing); err != nil {
		return err
	}
	if existing != 0 {
		return ErrConflict
	}
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM interaction_skill_run_requests)+(SELECT count(*) FROM interaction_skill_controls),(SELECT coalesce(sum(reserved_bytes),0) FROM interaction_skill_run_operations)+(SELECT coalesce(sum(reserved_bytes),0) FROM interaction_skill_controls)`).Scan(&count, &total); err != nil {
		return err
	}
	if count >= 2048 || total+4096 > 64<<20 {
		return ErrBudget
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO interaction_skill_controls VALUES(?,?,?,4096)`, request, id, fp); err != nil {
		return err
	}
	return tx.Commit()
}
