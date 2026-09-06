package notification

import (
	"context"
	"path/filepath"
	"testing"

	"agent/internal/storage"
)

func TestNotificationStoreListAndRead(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenSQLite(ctx, filepath.Join(t.TempDir(), "notification.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.Migrate(ctx, db.SQL); err != nil {
		t.Fatal(err)
	}
	store := Store{DB: db.SQL}
	if err := store.Put(ctx, Notification{ID: "notif-1", ProjectID: "default", TriggerID: "trigger-1", Title: "Title", Body: "Body", DedupKey: "dedup-1"}); err != nil {
		t.Fatal(err)
	}
	items, err := store.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != "unread" {
		t.Fatalf("items=%#v", items)
	}
	if err := store.MarkRead(ctx, "notif-1"); err != nil {
		t.Fatal(err)
	}
	items, err = store.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Status != "read" || items[0].ReadAt == nil {
		t.Fatalf("items=%#v", items)
	}
}
