package config

import (
	"errors"
	"fmt"
)

var (
	ErrMissingAppName        = errors.New("app.name is required")
	ErrMissingStorageDriver  = errors.New("storage.driver is required")
	ErrMissingSQLitePath     = errors.New("storage.sqlite.path is required when storage.driver is sqlite")
	ErrMissingPrivacyHMACEnv = errors.New("privacy.hmac_secret_env must resolve when an enabled public provider requires privacy gateway")
)

func (c Config) Validate() error {
	if c.ConfigVersion != 0 && c.ConfigVersion != 1 {
		return fmt.Errorf("unsupported config_version %d", c.ConfigVersion)
	}
	if c.App.Name == "" {
		return ErrMissingAppName
	}
	if c.Storage.Driver == "" {
		return ErrMissingStorageDriver
	}
	if c.Storage.Driver == "sqlite" && c.Storage.SQLite.Path == "" {
		return ErrMissingSQLitePath
	}
	if c.Storage.Driver != "sqlite" && c.Storage.Driver != "postgres" {
		return fmt.Errorf("unsupported storage.driver %q", c.Storage.Driver)
	}
	if c.Models.DefaultProvider != "" {
		if _, ok := c.Models.Providers[c.Models.DefaultProvider]; !ok {
			return fmt.Errorf("models.default_provider %q is not defined", c.Models.DefaultProvider)
		}
	}
	for id, provider := range c.Models.Providers {
		if provider.Type == "" {
			return fmt.Errorf("models.providers.%s.type is required", id)
		}
		if provider.IsEnabled() && provider.TrustLevel == "public_remote" && provider.RequirePrivacyGateway {
			if _, ok := EnvValue(c.Privacy.HMACSecretEnv); !ok {
				return ErrMissingPrivacyHMACEnv
			}
		}
	}
	if c.Agent.Leader.ModelID == "" {
		return errors.New("agent.leader.model_id is required")
	}
	models := make(map[string]struct{}, len(c.Models.Registry))
	for _, item := range c.Models.Registry {
		if item.ID == "" {
			return errors.New("models.registry[].id is required")
		}
		if item.Provider == "" {
			return fmt.Errorf("models.registry.%s.provider is required", item.ID)
		}
		if _, ok := c.Models.Providers[item.Provider]; !ok {
			return fmt.Errorf("models.registry.%s.provider %q is not defined", item.ID, item.Provider)
		}
		models[item.ID] = struct{}{}
	}
	if len(models) > 0 {
		if _, ok := models[c.Agent.Leader.ModelID]; !ok {
			return fmt.Errorf("agent.leader.model_id %q is not defined in models.registry", c.Agent.Leader.ModelID)
		}
		for role, cfg := range c.Agent.SubAgents {
			if cfg.ModelID == "" {
				continue
			}
			if _, ok := models[cfg.ModelID]; !ok {
				return fmt.Errorf("agent.subagents.%s.model_id %q is not defined in models.registry", role, cfg.ModelID)
			}
		}
	}
	if c.CodingAgents.DefaultBackend != "" {
		if _, ok := c.CodingAgents.Backends[c.CodingAgents.DefaultBackend]; !ok {
			return fmt.Errorf("coding_agents.default_backend %q is not defined", c.CodingAgents.DefaultBackend)
		}
	}
	for id, backend := range c.CodingAgents.Backends {
		if backend.Adapter == "" {
			return fmt.Errorf("coding_agents.backends.%s.adapter is required", id)
		}
		if backend.IsEnabled() && backend.InferenceTrust == "" {
			return fmt.Errorf("coding_agents.backends.%s.inference_trust is required", id)
		}
		if backend.IsEnabled() && backend.ExecutionLocation == "" {
			return fmt.Errorf("coding_agents.backends.%s.execution_location is required", id)
		}
	}
	return nil
}
