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
	if c.Agent.Planner.MaxNodes < 0 {
		return errors.New("agent.planner.max_nodes must be non-negative")
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
	if c.RAG.Vector.Enabled {
		if c.RAG.Vector.EmbeddingModelID == "" {
			return errors.New("rag.vector.embedding_model_id is required when vector is enabled")
		}
		if c.RAG.Vector.QdrantCollection == "" {
			return errors.New("rag.vector.qdrant_collection is required when vector is enabled")
		}
		if c.RAG.Vector.QdrantBaseURL == "" && c.RAG.Vector.QdrantBaseURLEnv == "" {
			return errors.New("rag.vector.qdrant_base_url or qdrant_base_url_env is required when vector is enabled")
		}
		if len(models) > 0 {
			if _, ok := models[c.RAG.Vector.EmbeddingModelID]; !ok {
				return fmt.Errorf("rag.vector.embedding_model_id %q is not defined in models.registry", c.RAG.Vector.EmbeddingModelID)
			}
		}
	}
	if c.RAG.Reranker.Enabled {
		if c.RAG.Reranker.ModelID == "" {
			return errors.New("rag.reranker.model_id is required when reranker is enabled")
		}
		if len(models) > 0 {
			if _, ok := models[c.RAG.Reranker.ModelID]; !ok {
				return fmt.Errorf("rag.reranker.model_id %q is not defined in models.registry", c.RAG.Reranker.ModelID)
			}
		}
	}
	if c.Security.API.MaxBodyBytes < 0 {
		return errors.New("security.api.max_body_bytes must be non-negative")
	}
	if c.Security.API.RateLimitPerMinute < 0 {
		return errors.New("security.api.rate_limit_per_minute must be non-negative")
	}
	resources := c.Reliability.Resources
	if resources.MaxConcurrentWorkflows < 0 || resources.MaxModelCalls < 0 || resources.MaxBrowserSessions < 0 ||
		resources.MaxCodingAgents < 0 || resources.MaxMCPCalls < 0 || resources.MaxOpenFiles < 0 || resources.MaxTemporaryDiskBytes < 0 {
		return errors.New("reliability.resources limits must be non-negative")
	}
	if c.Reliability.Disk.SoftLimitBytes < 0 || c.Reliability.Disk.HardLimitBytes < 0 {
		return errors.New("reliability.disk limits must be non-negative")
	}
	if c.Reliability.Disk.SoftLimitBytes > 0 && c.Reliability.Disk.HardLimitBytes > 0 && c.Reliability.Disk.SoftLimitBytes > c.Reliability.Disk.HardLimitBytes {
		return errors.New("reliability.disk.soft_limit_bytes must be <= hard_limit_bytes")
	}
	return nil
}
