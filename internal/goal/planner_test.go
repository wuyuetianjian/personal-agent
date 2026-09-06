package goal

import (
	"context"
	"errors"
	"testing"
)

func TestPlannerBoundsGoalIterations(t *testing.T) {
	_, err := (Planner{}).Plan(context.Background(), Goal{ID: "goal-1", MaxIterations: 11})
	if !errors.Is(err, ErrGoalIterationLimit) {
		t.Fatalf("Plan() error=%v", err)
	}
	milestones, err := (Planner{}).Plan(context.Background(), Goal{ID: "goal-1", MaxIterations: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(milestones) != 3 {
		t.Fatalf("milestones=%#v", milestones)
	}
}
