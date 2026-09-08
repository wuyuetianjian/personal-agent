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
	check := time.Now().UTC()
	transition := check.Add(-time.Minute)
	notification := check.Add(time.Minute)
	st := State{TriggerID: "trigger-1", NextFireAt: &next, LastEventHash: "hash", PreviousState: "false", CurrentState: "true", LastCheckAt: &check, LastTransitionAt: &transition, LastNotificationAt: &notification, StateJSON: `{"cursor":"1"}`}
	if err := store.PutState(ctx, st); err != nil {
		t.Fatal(err)
	}
	gotState, err := store.GetState(ctx, "trigger-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotState.NextFireAt == nil || gotState.LastEventHash != "hash" || gotState.StateJSON == "" || gotState.PreviousState != "false" || gotState.CurrentState != "true" || gotState.LastCheckAt == nil || gotState.LastTransitionAt == nil || gotState.LastNotificationAt == nil {
		t.Fatalf("state=%#v", gotState)
	}
	if err := store.PutHistory(ctx, History{ID: "hist-1", TriggerID: "trigger-1", WorkflowID: "wf-1", Status: "completed", Message: "ok"}); err != nil {
		t.Fatal(err)
	}
	history, err := store.History(ctx, "trigger-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].WorkflowID != "wf-1" {
		t.Fatalf("history=%#v", history)
	}
	if err := store.PutDeadLetter(ctx, DeadLetter{ID: "dlq-1", SourceID: "trigger-1", SourceType: "trigger", Reason: "failed", Payload: []byte("payload")}); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonRunNowAndRecover(t *testing.T) {
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
		ID:        "trigger-run",
		ProjectID: "project-1",
		Type:      TypeInterval,
		Enabled:   true,
		Schedule:  ScheduleSpec{Interval: time.Minute},
		PolicyRef: "default",
		BudgetRef: "local",
	}
	if err := store.Put(ctx, tr); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC)
	daemon := Daemon{Store: store, Starter: fixedStarter("wf-trigger"), Now: func() time.Time { return now }}
	if _, err := daemon.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := daemon.RunNow(ctx, "trigger-run"); err != nil {
		t.Fatal(err)
	}
	history, err := store.History(ctx, "trigger-run", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].WorkflowID != "wf-trigger" || history[0].Status != "completed" {
		t.Fatalf("history=%#v", history)
	}
}

func TestConditionWatcherPersistsCheckTransitionAndNotification(t *testing.T) {
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
		ID:        "condition-watch",
		ProjectID: "project-1",
		Type:      TypeConditionWatch,
		Enabled:   true,
		Condition: ConditionSpec{Evaluator: "test"},
	}
	if err := store.Put(ctx, tr); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 8, 1, 2, 3, 0, time.UTC)
	daemon := Daemon{Store: store, Starter: fixedStarter("wf-condition"), Now: func() time.Time { return now }}
	started, err := daemon.Tick(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if started != 1 {
		t.Fatalf("started=%d, want 1", started)
	}
	st, err := store.GetState(ctx, "condition-watch")
	if err != nil {
		t.Fatal(err)
	}
	if st.PreviousState != "false" || st.CurrentState != "true" || st.LastCheckAt == nil || st.LastTransitionAt == nil || st.LastNotificationAt == nil {
		t.Fatalf("state=%#v, want condition transition and notification timestamps", st)
	}
}

func TestDaemonStartRecoversAndTicksUntilShutdown(t *testing.T) {
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
		ID:        "condition-start",
		ProjectID: "project-1",
		Type:      TypeConditionWatch,
		Enabled:   true,
		Condition: ConditionSpec{Evaluator: "test"},
	}
	if err := store.Put(ctx, tr); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 8, 2, 3, 4, 0, time.UTC)
	daemonCtx, cancel := context.WithCancel(ctx)
	Daemon{Store: store, Starter: fixedStarter("wf-start"), Now: func() time.Time { return now }}.Start(daemonCtx, time.Hour)
	defer cancel()

	deadline := time.After(time.Second)
	for {
		history, err := store.History(ctx, "condition-start", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(history) == 1 {
			if history[0].WorkflowID != "wf-start" || history[0].Status != "completed" {
				t.Fatalf("history=%#v", history)
			}
			st, err := store.GetState(ctx, "condition-start")
			if err != nil {
				t.Fatal(err)
			}
			if st.LastCheckAt == nil || st.LastNotificationAt == nil {
				t.Fatalf("state=%#v, want recovered watcher state", st)
			}
			cancel()
			return
		}
		select {
		case <-deadline:
			t.Fatal("daemon did not recover and tick")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type fixedStarter string

func (s fixedStarter) StartTriggerWorkflow(ctx context.Context, t Trigger) (string, error) {
	return string(s), ctx.Err()
}
