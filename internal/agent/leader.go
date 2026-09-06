package agent

import (
	"context"
	"time"
)

type LeaderAgent interface {
	Plan(ctx context.Context, input string) ([]TaskNode, error)
	Synthesize(ctx context.Context, results []Result) (Result, error)
}

type TaskNode struct {
	TaskID       string
	ID           string
	Type         string
	Role         Role
	Input        string
	Dependencies []string
	Timeout      time.Duration
	MaxAttempts  int
	Metadata     map[string]string
}
