package codingagent

import (
	"context"
	"sort"
)

type Registry struct {
	adapters map[string]Adapter
	order    []string
}

func NewRegistry() *Registry {
	return &Registry{adapters: map[string]Adapter{}}
}

func (r *Registry) Register(adapter Adapter) {
	if adapter == nil {
		return
	}
	id := adapter.ID()
	if _, ok := r.adapters[id]; !ok {
		r.order = append(r.order, id)
	}
	r.adapters[id] = adapter
}

func (r *Registry) Lookup(id string) (Adapter, bool) {
	if r == nil {
		return nil, false
	}
	adapter, ok := r.adapters[id]
	return adapter, ok
}

func (r *Registry) Select(ctx context.Context, req Request, preferred []string) (Adapter, error) {
	if r == nil {
		return nil, ErrNoBackendAvailable
	}
	ids := append([]string(nil), preferred...)
	if len(ids) == 0 {
		ids = append(ids, r.order...)
	}
	for _, id := range ids {
		adapter, ok := r.adapters[id]
		if !ok {
			continue
		}
		cfg := adapter.Config()
		if err := ValidatePolicy(cfg, req); err != nil {
			continue
		}
		if err := adapter.Health(ctx); err != nil {
			continue
		}
		return adapter, nil
	}
	return nil, ErrNoBackendAvailable
}

func (r *Registry) IDs() []string {
	if r == nil {
		return nil
	}
	ids := append([]string(nil), r.order...)
	sort.Strings(ids)
	return ids
}

func ValidatePolicy(cfg BackendConfig, req Request) error {
	if !cfg.Enabled {
		return ErrBackendDisabled
	}
	if !supportsAll(cfg.Capabilities, req.Required) {
		return ErrCapabilityUnsupported
	}
	if cfg.PrivacyPolicy == PrivacyDenyConfidential &&
		req.Privacy == RepositoryConfidential &&
		cfg.InferenceTrust == InferencePublicRemote {
		return ErrPrivacyDenied
	}
	if req.AllowWrite && !req.AllowDirectWrites && req.WorkspacePath == "" && !cfg.AllowDirectWrites {
		return ErrDirectWriteDenied
	}
	return nil
}

func supportsAll(have []Capability, required []Capability) bool {
	if len(required) == 0 {
		return true
	}
	set := map[Capability]bool{}
	for _, capability := range have {
		set[capability] = true
	}
	for _, capability := range required {
		if !set[capability] {
			return false
		}
	}
	return true
}
