package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadExampleConfig(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "configs", "config.example.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.App.Name != "personal-agent" {
		t.Fatalf("App.Name = %q", cfg.App.Name)
	}
	if cfg.Server.RequestTimeout.Duration == 0 {
		t.Fatal("RequestTimeout was not parsed")
	}
}

func TestValidateRequiresHMACForEnabledPublicProvider(t *testing.T) {
	t.Setenv("PERSONAL_AGENT_PRIVACY_HMAC_SECRET", "")
	enabled := true
	cfg := Config{
		App: AppConfig{Name: "personal-agent"},
		Storage: StorageConfig{
			Driver: "sqlite",
			SQLite: SQLiteConfig{Path: "test.db"},
		},
		Privacy: PrivacyConfig{HMACSecretEnv: "PERSONAL_AGENT_PRIVACY_HMAC_SECRET"},
		Models: ModelsConfig{
			DefaultProvider: "public",
			Providers: map[string]ProviderConfig{
				"public": {
					Enabled:               &enabled,
					Type:                  "openai_compatible",
					TrustLevel:            "public_remote",
					RequirePrivacyGateway: true,
				},
			},
		},
		Agent: AgentConfig{Leader: RoleModelConfig{ModelID: "model"}},
	}

	if err := cfg.Validate(); !errors.Is(err, ErrMissingPrivacyHMACEnv) {
		t.Fatalf("Validate() error = %v, want %v", err, ErrMissingPrivacyHMACEnv)
	}
}

func TestEnvValue(t *testing.T) {
	const key = "PERSONAL_AGENT_TEST_ENV_VALUE"
	t.Setenv(key, "value")
	if got, ok := EnvValue(key); !ok || got != "value" {
		t.Fatalf("EnvValue() = %q, %v", got, ok)
	}
	os.Unsetenv(key)
}
