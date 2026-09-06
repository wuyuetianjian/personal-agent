package codingagent

import (
	"os"

	"agent/internal/config"
)

func RegistryFromConfig(cfg config.CodingAgentsConfig, executor Executor) *Registry {
	registry := NewRegistry()
	for id, backend := range cfg.Backends {
		agentCfg := BackendConfig{
			ID:                id,
			Enabled:           backend.IsEnabled(),
			Adapter:           backend.Adapter,
			Binary:            configuredBinary(backend),
			Timeout:           backend.Timeout.Duration,
			Concurrency:       backend.Concurrency,
			ExecutionLocation: ExecutionLocation(backend.ExecutionLocation),
			InferenceTrust:    InferenceTrust(backend.InferenceTrust),
			PrivacyPolicy:     privacyPolicy(backend.PrivacyPolicy),
			Capabilities:      capabilities(backend.Capabilities),
			AllowDirectWrites: backend.AllowDirectWrites,
		}
		switch backend.Adapter {
		case "claude":
			registry.Register(NewClaudeAdapter(agentCfg, executor))
		default:
			registry.Register(NewCodexAdapter(agentCfg, executor))
		}
	}
	return registry
}

func configuredBinary(backend config.CodingAgentBackendConfig) string {
	if backend.BinaryEnv != "" {
		if value, ok := os.LookupEnv(backend.BinaryEnv); ok && value != "" {
			return value
		}
	}
	return backend.Binary
}

func capabilities(values []string) []Capability {
	out := make([]Capability, 0, len(values))
	for _, value := range values {
		out = append(out, Capability(value))
	}
	return out
}

func privacyPolicy(value string) PrivacyPolicy {
	if value == "" {
		return PrivacyDenyConfidential
	}
	return PrivacyPolicy(value)
}
