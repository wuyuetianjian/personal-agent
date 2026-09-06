package event

import (
	"agent/internal/storage"
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestBusStoresAndPublishesUntrustedEvent(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenSQLite(ctx, filepath.Join(t.TempDir(), "event.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.Migrate(ctx, db.SQL); err != nil {
		t.Fatal(err)
	}
	store := Store{DB: db.SQL}
	bus := NewBus(&store)
	var handled Event
	bus.Subscribe(func(ctx context.Context, e Event) error {
		handled = e
		return nil
	})
	e := Event{
		ID:           "evt-1",
		Source:       "push",
		Type:         "repo.changed",
		ProjectID:    "project-1",
		Payload:      []byte(`{"path":"README.md"}`),
		PrivacyClass: "local_private",
		TrustLevel:   "untrusted",
		DedupKey:     DedupKey("push", "repo.changed", "README.md"),
	}
	if err := bus.Publish(ctx, e); err != nil {
		t.Fatal(err)
	}
	if handled.ID != "evt-1" || handled.TrustLevel != "untrusted" {
		t.Fatalf("handled=%#v", handled)
	}
	got, err := store.Get(ctx, "evt-1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Payload) != `{"path":"README.md"}` {
		t.Fatalf("payload=%q", got.Payload)
	}
}

func TestEventDedupRejectsDuplicateKey(t *testing.T) {
	ctx := context.Background()
	db, _ := storage.OpenSQLite(ctx, filepath.Join(t.TempDir(), "event.db"))
	defer db.Close()
	if err := storage.Migrate(ctx, db.SQL); err != nil {
		t.Fatal(err)
	}
	store := Store{DB: db.SQL}
	first := Event{ID: "evt-1", Source: "push", Type: "same", ProjectID: "p", Payload: []byte(`{}`), PrivacyClass: "local_private", TrustLevel: "untrusted", DedupKey: "same"}
	second := first
	second.ID = "evt-2"
	if err := store.Put(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, second); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate err=%v", err)
	}
}
