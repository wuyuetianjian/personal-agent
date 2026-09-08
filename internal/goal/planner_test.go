package goal

import (
	"agent/internal/storage"
	"context"
	"errors"
	"path/filepath"
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

func TestGoalStorePersistsRuntimeStateAndReevaluates(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenSQLite(ctx, filepath.Join(t.TempDir(), "goal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.Migrate(ctx, db.SQL); err != nil {
		t.Fatal(err)
	}
	store := Store{DB: db.SQL}
	if err := store.SaveGoal(ctx, Goal{
		ID:                 "goal-1",
		ProjectID:          "project-1",
		Title:              "Ship feature",
		MaxIterations:      3,
		WorkflowID:         "wf-goal",
		CompletionCriteria: "tests pass",
		BudgetPolicy:       `{"max_cost_usd":1}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMilestones(ctx, []Milestone{
		{ID: "m1", GoalID: "goal-1", Title: "Plan", Order: 1, WorkflowID: "wf-plan", CompletionCriteria: "plan approved"},
		{ID: "m2", GoalID: "goal-1", Title: "Build", Order: 2, Dependencies: []string{"m1"}, WorkflowID: "wf-build", CompletionCriteria: "tests pass"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Pause(ctx, "goal-1"); err != nil {
		t.Fatal(err)
	}
	paused, err := store.GetGoal(ctx, "goal-1")
	if err != nil {
		t.Fatal(err)
	}
	if paused.Status != "paused" || paused.WorkflowID != "wf-goal" || paused.CompletionCriteria != "tests pass" || paused.BudgetPolicy == "" {
		t.Fatalf("paused goal=%#v", paused)
	}
	if err := store.Resume(ctx, "goal-1"); err != nil {
		t.Fatal(err)
	}
	milestones, err := store.ListMilestones(ctx, "goal-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(milestones) != 2 || len(milestones[1].Dependencies) != 1 || milestones[1].Dependencies[0] != "m1" || milestones[1].WorkflowID != "wf-build" {
		t.Fatalf("milestones=%#v", milestones)
	}
	reevaluated, err := store.Reevaluate(ctx, Planner{MaxIterations: 3}, "goal-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(reevaluated) != 3 {
		t.Fatalf("reevaluated=%#v", reevaluated)
	}
	goal, err := store.GetGoal(ctx, "goal-1")
	if err != nil {
		t.Fatal(err)
	}
	if goal.Status != "active" || goal.LastEvaluatedAt == nil {
		t.Fatalf("goal=%#v, want resumed with evaluation timestamp", goal)
	}
}
