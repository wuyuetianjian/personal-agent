package trigger

import (
	"testing"
	"time"
)

func TestNextFireDisabledNeverFires(t *testing.T) {
	next, err := NextFire(Trigger{ID: "t", ProjectID: "p", Type: TypeInterval, Schedule: ScheduleSpec{Interval: time.Hour}}, State{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if next != nil {
		t.Fatalf("next=%v", next)
	}
}

func TestOneShotFiresOnceAndDisablesAfterSuccess(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	tr := Trigger{ID: "t", ProjectID: "p", Type: TypeOneShot, Enabled: true, Schedule: ScheduleSpec{OneShotAt: now.Add(time.Hour)}}
	next, err := NextFire(tr, State{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if next == nil || !next.Equal(now.Add(time.Hour)) {
		t.Fatalf("next=%v", next)
	}
	tr, st := MarkSuccess(tr, State{}, now.Add(time.Hour))
	if tr.Enabled || st.ConsecutiveFailures != 0 || st.LastSuccessAt == nil {
		t.Fatalf("trigger=%#v state=%#v", tr, st)
	}
}

func TestIntervalUsesFixedDelayAfterSuccess(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	last := now.Add(-30 * time.Minute)
	tr := Trigger{ID: "t", ProjectID: "p", Type: TypeInterval, Enabled: true, Schedule: ScheduleSpec{Interval: time.Hour}}
	next, err := NextFire(tr, State{LastSuccessAt: &last}, now)
	if err != nil {
		t.Fatal(err)
	}
	if next == nil || !next.Equal(last.Add(time.Hour)) {
		t.Fatalf("next=%v", next)
	}
}

func TestIntervalReturnsDueTimeAfterRestart(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	last := now.Add(-90 * time.Minute)
	tr := Trigger{ID: "t", ProjectID: "p", Type: TypeInterval, Enabled: true, Schedule: ScheduleSpec{Interval: time.Hour}}
	next, err := NextFire(tr, State{LastSuccessAt: &last}, now)
	if err != nil {
		t.Fatal(err)
	}
	want := last.Add(time.Hour)
	if next == nil || !next.Equal(want) {
		t.Fatalf("next=%v want=%v", next, want)
	}
}

func TestCronNextFireMinutePrecision(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 34, 20, 0, time.UTC)
	tr := Trigger{ID: "t", ProjectID: "p", Type: TypeCron, Enabled: true, Schedule: ScheduleSpec{Cron: "35 12 * * *"}}
	next, err := NextFire(tr, State{}, now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 6, 12, 35, 0, 0, time.UTC)
	if next == nil || !next.Equal(want) {
		t.Fatalf("next=%v want=%v", next, want)
	}
}

func TestNoiseControls(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	last := now.Add(-5 * time.Second)
	cooldownUntil := now.Add(time.Minute)
	st := State{LastFiredAt: &last, LastEventHash: "hash", CooldownUntil: &cooldownUntil}
	if !ShouldDebounce(st, now, 10*time.Second, "hash") {
		t.Fatal("expected debounce")
	}
	if !InCooldown(st, now) {
		t.Fatal("expected cooldown")
	}
	if ExecutionKey("t", now, "hash") != ExecutionKey("t", now, "hash") {
		t.Fatal("execution key must be stable")
	}
	delay, ok := BackoffDelay(BackoffPolicy{Initial: time.Minute, Multiplier: 2, Max: 10 * time.Minute, MaxAttempts: 4}, 3)
	if !ok || delay != 4*time.Minute {
		t.Fatalf("delay=%v ok=%v", delay, ok)
	}
	_, ok = BackoffDelay(BackoffPolicy{Initial: time.Minute, MaxAttempts: 3}, 3)
	if ok {
		t.Fatal("expected max attempts to stop retry")
	}
}
