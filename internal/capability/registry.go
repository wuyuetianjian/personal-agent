package capability

import (
	"context"
	"errors"
	"sort"
	"sync"
)

var (
	ErrDuplicateID = errors.New("duplicate capability id")
	ErrMissingID   = errors.New("capability id is required")
	ErrNotFound    = errors.New("capability not found")
)

type Registry struct {
	mu    sync.RWMutex
	items map[string]Capability
}

func NewRegistry(items ...Capability) (*Registry, error) {
	r := &Registry{items: make(map[string]Capability)}
	for _, item := range items {
		if err := r.Register(item); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Registry) Register(item Capability) error {
	if item.ID == "" {
		return ErrMissingID
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[item.ID]; ok {
		return ErrDuplicateID
	}
	if item.Health == "" {
		item.Health = HealthUnknown
	}
	r.items[item.ID] = clone(item)
	return nil
}

func (r *Registry) UpdateHealth(id string, health Health) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.items[id]
	if !ok {
		return ErrNotFound
	}
	item.Health = health
	r.items[id] = item
	return nil
}

func (r *Registry) Lookup(id string) (Capability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.items[id]
	return clone(item), ok
}

func (r *Registry) List(kind Kind) []Capability {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Capability, 0, len(r.items))
	for _, item := range r.items {
		if kind != "" && item.Kind != kind {
			continue
		}
		out = append(out, clone(item))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Registry) Health(ctx context.Context, id string) (Health, error) {
	if err := ctx.Err(); err != nil {
		return HealthUnknown, err
	}
	item, ok := r.Lookup(id)
	if !ok {
		return HealthUnknown, ErrNotFound
	}
	return item.Health, nil
}

func clone(item Capability) Capability {
	item.PrivacyClasses = append([]string(nil), item.PrivacyClasses...)
	item.Tags = append([]string(nil), item.Tags...)
	item.InputSchema = append([]byte(nil), item.InputSchema...)
	item.OutputSchema = append([]byte(nil), item.OutputSchema...)
	return item
}
