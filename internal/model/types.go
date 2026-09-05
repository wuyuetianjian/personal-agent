package model

import (
	"errors"
	"fmt"
	"sort"

	"agent/internal/config"
)

type TrustLevel string

const (
	TrustLocalPrivate   TrustLevel = "local_private"
	TrustLocalSandboxed TrustLevel = "local_sandboxed"
	TrustTrustedRemote  TrustLevel = "trusted_remote"
	TrustPublicRemote   TrustLevel = "public_remote"
)

type Capability string

const (
	CapabilityChat             Capability = "chat"
	CapabilityToolCalling      Capability = "tool_calling"
	CapabilityJSONSchema       Capability = "json_schema"
	CapabilityVision           Capability = "vision"
	CapabilityEmbedding        Capability = "embedding"
	CapabilityRerank           Capability = "rerank"
	CapabilityLongContext      Capability = "long_context"
	CapabilityBrowserReasoning Capability = "browser_reasoning"
)

var (
	ErrDuplicateModelID = errors.New("duplicate model id")
	ErrModelNotFound    = errors.New("model not found")
	ErrNoMatchingModel  = errors.New("no matching model")
)

type ProviderMetadata struct {
	ID                    string
	Type                  string
	BaseURL               string
	BaseURLEnv            string
	APIKeyEnv             string
	TrustLevel            TrustLevel
	RequirePrivacyGateway bool
	Enabled               bool
}

type ModelMetadata struct {
	ID            string
	ProviderID    string
	Provider      ProviderMetadata
	Model         string
	TrustLevel    TrustLevel
	ContextWindow int
	Capabilities  map[Capability]bool
	Pricing       Pricing
}

type Pricing struct {
	InputPer1M  float64
	OutputPer1M float64
}

type Registry struct {
	providers map[string]ProviderMetadata
	models    map[string]ModelMetadata
}

func NewRegistry(cfg config.ModelsConfig) (*Registry, error) {
	registry := &Registry{
		providers: make(map[string]ProviderMetadata, len(cfg.Providers)),
		models:    make(map[string]ModelMetadata, len(cfg.Registry)),
	}
	for id, provider := range cfg.Providers {
		trust, err := ParseTrustLevel(provider.TrustLevel)
		if err != nil {
			return nil, fmt.Errorf("provider %s: %w", id, err)
		}
		registry.providers[id] = ProviderMetadata{
			ID:                    id,
			Type:                  provider.Type,
			BaseURL:               provider.BaseURL,
			BaseURLEnv:            provider.BaseURLEnv,
			APIKeyEnv:             provider.APIKeyEnv,
			TrustLevel:            trust,
			RequirePrivacyGateway: provider.RequirePrivacyGateway,
			Enabled:               provider.IsEnabled(),
		}
	}
	for _, item := range cfg.Registry {
		if _, exists := registry.models[item.ID]; exists {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateModelID, item.ID)
		}
		provider, ok := registry.providers[item.Provider]
		if !ok {
			return nil, fmt.Errorf("model %s references unknown provider %s", item.ID, item.Provider)
		}
		trust, err := ParseTrustLevel(item.TrustLevel)
		if err != nil {
			return nil, fmt.Errorf("model %s: %w", item.ID, err)
		}
		capabilities := make(map[Capability]bool, len(item.Capabilities))
		for _, raw := range item.Capabilities {
			capability, err := ParseCapability(raw)
			if err != nil {
				return nil, fmt.Errorf("model %s: %w", item.ID, err)
			}
			capabilities[capability] = true
		}
		registry.models[item.ID] = ModelMetadata{
			ID:            item.ID,
			ProviderID:    item.Provider,
			Provider:      provider,
			Model:         item.Model,
			TrustLevel:    trust,
			ContextWindow: item.ContextWindow,
			Capabilities:  capabilities,
			Pricing: Pricing{
				InputPer1M:  item.Pricing.InputPer1M,
				OutputPer1M: item.Pricing.OutputPer1M,
			},
		}
	}
	return registry, nil
}

func (r *Registry) Model(id string) (ModelMetadata, error) {
	model, ok := r.models[id]
	if !ok {
		return ModelMetadata{}, fmt.Errorf("%w: %s", ErrModelNotFound, id)
	}
	return model, nil
}

func (r *Registry) Models() []ModelMetadata {
	models := make([]ModelMetadata, 0, len(r.models))
	for _, model := range r.models {
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}

func ParseTrustLevel(value string) (TrustLevel, error) {
	switch TrustLevel(value) {
	case TrustLocalPrivate, TrustLocalSandboxed, TrustTrustedRemote, TrustPublicRemote:
		return TrustLevel(value), nil
	default:
		return "", fmt.Errorf("unknown trust level %q", value)
	}
}

func ParseCapability(value string) (Capability, error) {
	switch Capability(value) {
	case CapabilityChat, CapabilityToolCalling, CapabilityJSONSchema, CapabilityVision,
		CapabilityEmbedding, CapabilityRerank, CapabilityLongContext, CapabilityBrowserReasoning:
		return Capability(value), nil
	default:
		return "", fmt.Errorf("unknown capability %q", value)
	}
}

func TrustAllows(max TrustLevel, candidate TrustLevel) bool {
	return trustRank(candidate) <= trustRank(max)
}

func trustRank(level TrustLevel) int {
	switch level {
	case TrustLocalPrivate:
		return 0
	case TrustLocalSandboxed:
		return 1
	case TrustTrustedRemote:
		return 2
	case TrustPublicRemote:
		return 3
	default:
		return 99
	}
}
