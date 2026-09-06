package trigger

import (
	"errors"
	"sort"
	"sync"
)

var ErrDuplicateTrigger = errors.New("trigger already exists")

type Registry struct {
	mu       sync.RWMutex
	triggers map[string]Trigger
}

func NewRegistry() *Registry {
	return &Registry{triggers: map[string]Trigger{}}
}

func (r *Registry) Add(t Trigger) error {
	if err := Validate(t); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.triggers[t.ID]; ok {
		return ErrDuplicateTrigger
	}
	r.triggers[t.ID] = t
	return nil
}

func (r *Registry) Get(id string) (Trigger, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.triggers[id]
	return t, ok
}

func (r *Registry) ListEnabled() []Trigger {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []Trigger{}
	for _, t := range r.triggers {
		if t.Enabled {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
