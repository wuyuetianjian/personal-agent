package event

import (
	"context"
	"sync"
)

type Handler func(context.Context, Event) error

type Bus struct {
	mu       sync.RWMutex
	handlers []Handler
	store    *Store
}

func NewBus(store *Store) *Bus {
	return &Bus{store: store}
}

func (b *Bus) Subscribe(h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = append(b.handlers, h)
}

func (b *Bus) Publish(ctx context.Context, e Event) error {
	e = Normalize(e)
	if err := e.Validate(); err != nil {
		return err
	}
	if b.store != nil {
		if err := b.store.Put(ctx, e); err != nil {
			return err
		}
	}
	b.mu.RLock()
	handlers := append([]Handler(nil), b.handlers...)
	b.mu.RUnlock()
	for _, h := range handlers {
		if err := h(ctx, e); err != nil {
			return err
		}
	}
	return nil
}
