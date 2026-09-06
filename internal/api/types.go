package api

import (
	"context"

	"agent/internal/orchestrator"
	"agent/internal/permission"
	"agent/internal/storage"
)

type TaskStore interface {
	CreateTask(ctx context.Context, task storage.Task) error
	ListTasks(ctx context.Context, limit int) ([]storage.Task, error)
	GetTask(ctx context.Context, id string) (storage.Task, error)
	CancelTask(ctx context.Context, id string) error
}

type EventSource interface {
	Events() []orchestrator.Event
}

type ConfirmationStore interface {
	Request(ctx context.Context, request permission.Request) (string, error)
	IsApproved(ctx context.Context, confirmationID string) (bool, error)
	Approve(ctx context.Context, confirmationID string) error
	Deny(ctx context.Context, confirmationID string) error
	RequestByID(id string) (permission.Request, bool)
}

type ConfirmationStatusStore interface {
	Status(ctx context.Context, confirmationID string) (string, error)
}
