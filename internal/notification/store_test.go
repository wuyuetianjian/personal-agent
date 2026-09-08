package notification

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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
	if len(items) != 1 || items[0].Status != "unread" || items[0].Severity != "info" || items[0].DeliveryState != "inbox" {
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

func TestNotificationPolicyDedupSeverityAndQuietHours(t *testing.T) {
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
	createdAt := time.Date(2026, 9, 8, 2, 30, 0, 0, time.UTC)
	inserted, err := store.PutWithPolicy(ctx, Notification{
		ID:        "notif-quiet",
		ProjectID: "default",
		TriggerID: "trigger-quiet",
		Title:     "Title",
		Body:      "Body",
		DedupKey:  "dedup-quiet",
		Severity:  "warning",
		CreatedAt: createdAt,
	}, Policy{QuietHoursStart: "22:00", QuietHoursEnd: "07:00"})
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Fatal("first notification was not inserted")
	}
	inserted, err = store.PutWithPolicy(ctx, Notification{
		ID:        "notif-duplicate",
		ProjectID: "default",
		TriggerID: "trigger-quiet",
		Title:     "Duplicate",
		Body:      "Body",
		DedupKey:  "dedup-quiet",
		Severity:  "critical",
		CreatedAt: createdAt,
	}, Policy{QuietHoursStart: "22:00", QuietHoursEnd: "07:00"})
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		t.Fatal("duplicate notification was inserted")
	}
	items, err := store.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%#v, want deduped single notification", items)
	}
	item := items[0]
	if item.Severity != "warning" || item.DeliveryState != "quiet_hours" || item.DeliveryAfter == nil || !item.DeliveryAfter.After(createdAt) {
		t.Fatalf("item=%#v, want warning quiet-hours delivery state", item)
	}
}
