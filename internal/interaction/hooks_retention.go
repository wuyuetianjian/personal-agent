package interaction

import (
	"context"
	"database/sql"
	"time"
)

const hookOperationReservation = 65536
const hookRunReservation = 163840

func hookManagementCapacity(ctx context.Context, tx *sql.Tx, session, kind string) error {
	var ops, local, pending, localPending, bytes int
	if e := tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(session_id=?),0),coalesce(sum(state='pending' AND expires_at_ms>?),0),coalesce(sum(session_id=? AND state='pending' AND expires_at_ms>?),0),coalesce(sum(reserved_bytes),0) FROM interaction_hook_operations`, session, time.Now().UnixMilli(), session, time.Now().UnixMilli()).Scan(&ops, &local, &pending, &localPending, &bytes); e != nil {
		return e
	}
	if ops >= 256 || local >= 64 || pending >= 8 || localPending >= 4 || bytes+hookOperationReservation > 16<<20 {
		return ErrBudget
	}
	if kind == "import" {
		var n, l int
		if e := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM interaction_hook_revisions)+(SELECT count(*) FROM interaction_hook_operations WHERE kind='import' AND state='pending'),(SELECT count(*) FROM interaction_hook_revisions WHERE session_id=?)+(SELECT count(*) FROM interaction_hook_operations WHERE session_id=? AND kind='import' AND state='pending')`, session, session).Scan(&n, &l); e != nil {
			return e
		}
		if n >= 128 || l >= 32 {
			return ErrBudget
		}
	}
	return nil
}
