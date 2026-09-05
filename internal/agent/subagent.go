package agent

import "context"

type SubAgent interface {
	Role() Role
	Execute(ctx context.Context, node TaskNode) (Result, error)
}
