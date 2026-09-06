package browser

import (
	"context"
	"sync"
)

type ConfirmationChecker interface {
	IsConfirmed(ctx context.Context, confirmationID string, action Action) bool
}

type InMemoryConfirmationStore struct {
	mu       sync.RWMutex
	approved map[string]struct{}
}

func NewInMemoryConfirmationStore() *InMemoryConfirmationStore {
	return &InMemoryConfirmationStore{approved: make(map[string]struct{})}
}

func (s *InMemoryConfirmationStore) Approve(confirmationID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approved[confirmationID] = struct{}{}
}

func (s *InMemoryConfirmationStore) IsConfirmed(ctx context.Context, confirmationID string, _ Action) bool {
	if ctx.Err() != nil || confirmationID == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.approved[confirmationID]
	return ok
}
