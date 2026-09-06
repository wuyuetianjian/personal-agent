package orchestrator

import (
	"context"
	"sync"
	"time"

	"agent/internal/agent"
)

type EventType string

const (
	EventNodeReady     EventType = "orchestrator.node.ready"
	EventNodeStarted   EventType = "orchestrator.node.started"
	EventNodeRetrying  EventType = "orchestrator.node.retrying"
	EventNodeCompleted EventType = "orchestrator.node.completed"
	EventNodeFailed    EventType = "orchestrator.node.failed"
	EventNodeCancelled EventType = "orchestrator.node.cancelled"
	EventEarlyStopped  EventType = "orchestrator.early_stopped"
)

type Event struct {
	Type          EventType
	TaskID        string
	NodeID        string
	Role          agent.Role
	Attempt       int
	ErrorCategory agent.ErrorCategory
	ErrorMessage  string
	EvidenceIDs   []string
	RecordedAt    time.Time
}

type EvidenceBus interface {
	Publish(ctx context.Context, event Event) error
}

type InMemoryEvidenceBus struct {
	mu     sync.Mutex
	events []Event
}

func (b *InMemoryEvidenceBus) Publish(ctx context.Context, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.RecordedAt.IsZero() {
		event.RecordedAt = time.Now().UTC()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, event)
	return nil
}

func (b *InMemoryEvidenceBus) Events() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	events := make([]Event, len(b.events))
	copy(events, b.events)
	return events
}
