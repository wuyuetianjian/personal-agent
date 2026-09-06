package goal

import (
	"context"
	"errors"
)

var ErrGoalIterationLimit = errors.New("goal iteration limit reached")

type Goal struct {
	ID            string
	ProjectID     string
	Title         string
	Status        string
	MaxIterations int
}

type Milestone struct {
	ID     string
	GoalID string
	Title  string
	Order  int
}

type Planner struct {
	MaxIterations int
}

func (p Planner) Plan(ctx context.Context, g Goal) ([]Milestone, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit := g.MaxIterations
	if limit <= 0 {
		limit = p.MaxIterations
	}
	if limit <= 0 {
		limit = 3
	}
	if limit > 10 {
		return nil, ErrGoalIterationLimit
	}
	return []Milestone{
		{ID: g.ID + "-m1", GoalID: g.ID, Title: "Define local evidence", Order: 1},
		{ID: g.ID + "-m2", GoalID: g.ID, Title: "Run governed workflow", Order: 2},
		{ID: g.ID + "-m3", GoalID: g.ID, Title: "Verify result", Order: 3},
	}, nil
}
