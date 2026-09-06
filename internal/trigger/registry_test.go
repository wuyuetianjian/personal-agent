package trigger

import (
	"errors"
	"testing"
	"time"
)

func TestValidateTriggerRequirements(t *testing.T) {
	validAt := time.Now().UTC().Add(time.Hour)
	tests := []struct {
		name string
		tr   Trigger
	}{
		{"invalid type", Trigger{ID: "t", ProjectID: "p", Type: Type("bad")}},
		{"missing one shot", Trigger{ID: "t", ProjectID: "p", Type: TypeOneShot}},
		{"missing interval", Trigger{ID: "t", ProjectID: "p", Type: TypeInterval}},
		{"missing cron", Trigger{ID: "t", ProjectID: "p", Type: TypeCron}},
		{"missing event source", Trigger{ID: "t", ProjectID: "p", Type: TypeEvent, Event: EventSpec{Type: "x"}}},
		{"missing evaluator", Trigger{ID: "t", ProjectID: "p", Type: TypeConditionWatch}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Validate(tt.tr); !errors.Is(err, ErrInvalidTrigger) {
				t.Fatalf("Validate() err=%v", err)
			}
		})
	}
	if err := Validate(Trigger{ID: "t", ProjectID: "p", Type: TypeOneShot, Schedule: ScheduleSpec{OneShotAt: validAt}}); err != nil {
		t.Fatalf("valid one-shot: %v", err)
	}
}

func TestRegistryListsOnlyEnabled(t *testing.T) {
	r := NewRegistry()
	if err := r.Add(Trigger{ID: "disabled", ProjectID: "p", Type: TypeManual}); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(Trigger{ID: "enabled", ProjectID: "p", Type: TypeManual, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	got := r.ListEnabled()
	if len(got) != 1 || got[0].ID != "enabled" {
		t.Fatalf("enabled=%#v", got)
	}
	if err := r.Add(Trigger{ID: "enabled", ProjectID: "p", Type: TypeManual, Enabled: true}); !errors.Is(err, ErrDuplicateTrigger) {
		t.Fatalf("duplicate err=%v", err)
	}
}
