package reliability

import (
	"context"
	"sync"
)

type ShutdownStep func(context.Context) error

type ShutdownCoordinator struct {
	mu        sync.Mutex
	accepting bool
	steps     []ShutdownStep
}

func NewShutdownCoordinator() *ShutdownCoordinator {
	return &ShutdownCoordinator{accepting: true}
}

func (s *ShutdownCoordinator) Add(step ShutdownStep) {
	if s == nil || step == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = append(s.steps, step)
}

func (s *ShutdownCoordinator) Accepting() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accepting
}

func (s *ShutdownCoordinator) Shutdown(ctx context.Context) []error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.accepting = false
	steps := append([]ShutdownStep(nil), s.steps...)
	s.mu.Unlock()
	var errs []error
	for i := len(steps) - 1; i >= 0; i-- {
		if err := steps[i](ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
