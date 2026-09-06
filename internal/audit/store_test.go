package audit

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"agent/internal/storage"
)

func TestStoreAppendRedactsDurableAudit(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenSQLite(ctx, filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.Migrate(ctx, db.SQL); err != nil {
		t.Fatal(err)
	}
	store := Store{DB: db.SQL}
	if err := store.Append(ctx, Record{
		ID:     "audit-1",
		Actor:  "operator",
		Action: "approval",
		Target: "https://example.test?token=secret",
		Status: "approved",
		Reason: "password=supersecret",
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	records, err := store.List(ctx, 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	got := records[0].Target + records[0].Reason
	if strings.Contains(got, "secret") || strings.Contains(got, "password=") || strings.Contains(got, "token=") {
		t.Fatalf("audit leaked secret fields: %+v", records[0])
	}
}
