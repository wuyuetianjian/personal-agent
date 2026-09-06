package permission

import (
	"context"
	"sync"
)

type ConfirmationStore interface {
	Request(ctx context.Context, request Request) (string, error)
	IsApproved(ctx context.Context, confirmationID string) (bool, error)
	Approve(ctx context.Context, confirmationID string) error
}

type InMemoryConfirmationStore struct {
	mu       sync.RWMutex
	requests map[string]Request
	approved map[string]struct{}
}

func NewInMemoryConfirmationStore() *InMemoryConfirmationStore {
	return &InMemoryConfirmationStore{
		requests: make(map[string]Request),
		approved: make(map[string]struct{}),
	}
}

func (s *InMemoryConfirmationStore) Request(ctx context.Context, request Request) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	id := request.ID
	if id == "" {
		id = "confirm_" + request.TaskID + "_" + string(request.Action)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests[id] = request
	return id, nil
}

func (s *InMemoryConfirmationStore) IsApproved(ctx context.Context, confirmationID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.approved[confirmationID]
	return ok, nil
}

func (s *InMemoryConfirmationStore) Approve(ctx context.Context, confirmationID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approved[confirmationID] = struct{}{}
	return nil
}

func (s *InMemoryConfirmationStore) RequestByID(id string) (Request, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	request, ok := s.requests[id]
	return request, ok
}
