package memory

import (
	"context"
	"path/filepath"
	"testing"

	"agent/internal/storage"
)

func TestStoreAppendAndRecent(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenSQLite(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	defer db.Close()
	if err := storage.Migrate(ctx, db.SQL); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	store := NewStore(db.SQL)
	if err := store.Append(ctx, EpisodicEvent{
		ID:        "mem-1",
		TaskID:    "task-1",
		EventType: "user_message",
		Summary:   "hello",
		Payload:   map[string]string{"text": "hello"},
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	events, err := store.Recent(ctx, 10)
	if err != nil {
		t.Fatalf("Recent() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if events[0].Summary != "hello" {
		t.Fatalf("Summary = %q, want hello", events[0].Summary)
	}
}
