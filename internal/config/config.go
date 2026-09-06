package config

import "time"

type Config struct {
	ConfigVersion int                `yaml:"config_version"`
	App           AppConfig          `yaml:"app"`
	Server        ServerConfig       `yaml:"server"`
	Storage       StorageConfig      `yaml:"storage"`
	Privacy       PrivacyConfig      `yaml:"privacy"`
	Models        ModelsConfig       `yaml:"models"`
	Agent         AgentConfig        `yaml:"agent"`
	Browser       BrowserConfig      `yaml:"browser"`
	Permissions   PermissionsConfig  `yaml:"permissions"`
	CodingAgents  CodingAgentsConfig `yaml:"coding_agents"`
}

type AppConfig struct {
	Name        string `yaml:"name"`
	Environment string `yaml:"environment"`
	DataDir     string `yaml:"data_dir"`
}

type ServerConfig struct {
	Enabled        bool     `yaml:"enabled"`
	ListenAddr     string   `yaml:"listen_addr"`
	RequestTimeout Duration `yaml:"request_timeout"`
}

type StorageConfig struct {
	Driver   string         `yaml:"driver"`
	SQLite   SQLiteConfig   `yaml:"sqlite"`
	Postgres PostgresConfig `yaml:"postgres"`
}

type SQLiteConfig struct {
	Path string `yaml:"path"`
}

type PostgresConfig struct {
	DSNEnv string `yaml:"dsn_env"`
}

type PrivacyConfig struct {
	HMACSecretEnv             string `yaml:"hmac_secret_env"`
	FailClosedForPublicModels bool   `yaml:"fail_closed_for_public_models"`
}

type ModelsConfig struct {
	DefaultProvider string                    `yaml:"default_provider"`
	Providers       map[string]ProviderConfig `yaml:"providers"`
	Registry        []ModelConfig             `yaml:"registry"`
}

type ProviderConfig struct {
	Enabled               *bool  `yaml:"enabled"`
	Type                  string `yaml:"type"`
	BaseURL               string `yaml:"base_url"`
	BaseURLEnv            string `yaml:"base_url_env"`
	APIKeyEnv             string `yaml:"api_key_env"`
	TrustLevel            string `yaml:"trust_level"`
	RequirePrivacyGateway bool   `yaml:"require_privacy_gateway"`
}

func (c ProviderConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

type ModelConfig struct {
	ID            string        `yaml:"id"`
	Provider      string        `yaml:"provider"`
	Model         string        `yaml:"model"`
	TrustLevel    string        `yaml:"trust_level"`
	ContextWindow int           `yaml:"context_window"`
	Capabilities  []string      `yaml:"capabilities"`
	Pricing       PricingConfig `yaml:"pricing"`
}

type PricingConfig struct {
	InputPer1M  float64 `yaml:"input_per_1m"`
	OutputPer1M float64 `yaml:"output_per_1m"`
}

type AgentConfig struct {
	Leader    RoleModelConfig            `yaml:"leader"`
	SubAgents map[string]RoleModelConfig `yaml:"subagents"`
}

type RoleModelConfig struct {
	ModelID            string  `yaml:"model_id"`
	Temperature        float64 `yaml:"temperature"`
	MaxOutputTokens    int     `yaml:"max_output_tokens"`
	TrustLevelRequired string  `yaml:"trust_level_required"`
}

type BrowserConfig struct {
	Enabled       bool                `yaml:"enabled"`
	Runtime       string              `yaml:"runtime"`
	ScreenshotDir string              `yaml:"screenshot_dir"`
	ProfileReuse  BrowserProfileReuse `yaml:"profile_reuse"`
}

type BrowserProfileReuse struct {
	Enabled        bool     `yaml:"enabled"`
	ProfileDirEnv  string   `yaml:"profile_dir_env"`
	AllowedDomains []string `yaml:"allowed_domains"`
	SessionTTL     Duration `yaml:"session_ttl"`
}

type PermissionsConfig struct {
	Default string                 `yaml:"default"`
	Rules   []PermissionRuleConfig `yaml:"rules"`
}

type PermissionRuleConfig struct {
	Action   string `yaml:"action"`
	Decision string `yaml:"decision"`
	HighRisk bool   `yaml:"high_risk"`
}

type CodingAgentsConfig struct {
	DefaultBackend string                              `yaml:"default_backend"`
	Backends       map[string]CodingAgentBackendConfig `yaml:"backends"`
}

type CodingAgentBackendConfig struct {
	Enabled           *bool    `yaml:"enabled"`
	Adapter           string   `yaml:"adapter"`
	Binary            string   `yaml:"binary"`
	BinaryEnv         string   `yaml:"binary_env"`
	Timeout           Duration `yaml:"timeout"`
	Concurrency       int      `yaml:"concurrency"`
	ExecutionLocation string   `yaml:"execution_location"`
	InferenceTrust    string   `yaml:"inference_trust"`
	PrivacyPolicy     string   `yaml:"privacy_policy"`
	Capabilities      []string `yaml:"capabilities"`
	AllowDirectWrites bool     `yaml:"allow_direct_writes"`
}

func (c CodingAgentBackendConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

type Duration struct {
	time.Duration
}
