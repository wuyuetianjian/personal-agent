package trigger

import (
	"agent/internal/storage"
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestTriggerStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenSQLite(ctx, filepath.Join(t.TempDir(), "trigger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.Migrate(ctx, db.SQL); err != nil {
		t.Fatal(err)
	}
	store := Store{DB: db.SQL}
	tr := Trigger{
		ID:        "trigger-1",
		ProjectID: "project-1",
		Type:      TypeInterval,
		Enabled:   true,
		SkillID:   "daily-brief",
		Schedule:  ScheduleSpec{Interval: time.Hour, Timezone: "UTC"},
		PolicyRef: "default",
		BudgetRef: "local",
	}
	if err := store.Put(ctx, tr); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "trigger-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Schedule.Interval != time.Hour || got.SkillID != "daily-brief" {
		t.Fatalf("got=%#v", got)
	}
	enabled, err := store.ListEnabled(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 1 || enabled[0].ID != "trigger-1" {
		t.Fatalf("enabled=%#v", enabled)
	}
	next := time.Now().UTC().Add(time.Hour)
	st := State{TriggerID: "trigger-1", NextFireAt: &next, LastEventHash: "hash", StateJSON: `{"cursor":"1"}`}
	if err := store.PutState(ctx, st); err != nil {
		t.Fatal(err)
	}
	gotState, err := store.GetState(ctx, "trigger-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotState.NextFireAt == nil || gotState.LastEventHash != "hash" || gotState.StateJSON == "" {
		t.Fatalf("state=%#v", gotState)
	}
}
