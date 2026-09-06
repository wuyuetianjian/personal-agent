package permission

import (
	"context"
	"path/filepath"
	"testing"

	"agent/internal/storage"
)

func TestSQLiteConfirmationStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenSQLite(ctx, filepath.Join(t.TempDir(), "confirm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.Migrate(ctx, db.SQL); err != nil {
		t.Fatal(err)
	}
	store := SQLiteConfirmationStore{DB: db.SQL}
	id, err := store.Request(ctx, Request{ID: "confirm-1", TaskID: "task-1", Action: ActionBrowserWrite, Risk: RiskHigh, EvidenceIDs: []string{"ev-1"}, Target: "local://target", ProposedEffect: "write"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "confirm-1" {
		t.Fatalf("id=%s", id)
	}
	request, ok := store.RequestByID("confirm-1")
	if !ok || request.EvidenceIDs[0] != "ev-1" {
		t.Fatalf("request=%#v ok=%t", request, ok)
	}
	pending, err := store.List(ctx, "pending", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending=%#v", pending)
	}
	if err := store.Approve(ctx, "confirm-1"); err != nil {
		t.Fatal(err)
	}
	approved, err := store.IsApproved(ctx, "confirm-1")
	if err != nil || !approved {
		t.Fatalf("approved=%t err=%v", approved, err)
	}
}
