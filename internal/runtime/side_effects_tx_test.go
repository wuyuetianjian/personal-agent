package runtime

import (
	"database/sql"
	"errors"
	"testing"
)

func TestSideEffectClaimTxRollbackParity(t *testing.T) {
	db := openRuntimeTestDB(t)
	j := SQLiteSideEffectStore{DB: db.SQL}
	record := SideEffectRecord{IdempotencyKey: "owned", WorkflowID: "workflow", NodeID: "node", CapabilityID: "read"}
	tx, e := db.SQL.BeginTx(t.Context(), nil)
	if e != nil {
		t.Fatal(e)
	}
	r, claimed, e := ClaimSideEffectTx(t.Context(), tx, record)
	if e != nil || !claimed || r.Status != "started" {
		t.Fatal(r, claimed, e)
	}
	tx.Rollback()
	if _, e = j.Get(t.Context(), record.IdempotencyKey); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal(e)
	}
	tx, _ = db.SQL.BeginTx(t.Context(), nil)
	r, claimed, e = ClaimSideEffectTx(t.Context(), tx, record)
	if e != nil || !claimed {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	same, claimed, e := j.Claim(t.Context(), record)
	if e != nil || claimed || same.WorkflowID != r.WorkflowID || !same.CreatedAt.Equal(r.CreatedAt) {
		t.Fatal(same, claimed, e)
	}
}
