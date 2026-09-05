package agent

import "context"

type LeaderAgent interface {
	Plan(ctx context.Context, input string) ([]TaskNode, error)
	Synthesize(ctx context.Context, results []Result) (Result, error)
}

type TaskNode struct {
	ID           string
	Type         string
	Role         Role
	Input        string
	Dependencies []string
}
