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
	RAG           RAGConfig          `yaml:"rag"`
	Browser       BrowserConfig      `yaml:"browser"`
	MCP           MCPConfig          `yaml:"mcp"`
	Tools         ToolsConfig        `yaml:"tools"`
	Permissions   PermissionsConfig  `yaml:"permissions"`
	CodingAgents  CodingAgentsConfig `yaml:"coding_agents"`
	Security      SecurityConfig     `yaml:"security"`
	Reliability   ReliabilityConfig  `yaml:"reliability"`
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
	Planner   PlannerConfig              `yaml:"planner"`
}

type RoleModelConfig struct {
	ModelID            string  `yaml:"model_id"`
	Temperature        float64 `yaml:"temperature"`
	MaxOutputTokens    int     `yaml:"max_output_tokens"`
	TrustLevelRequired string  `yaml:"trust_level_required"`
}

type PlannerConfig struct {
	Enabled  *bool `yaml:"enabled"`
	MaxNodes int   `yaml:"max_nodes"`
}

func (c PlannerConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

type RAGConfig struct {
	Vector   VectorRAGConfig   `yaml:"vector"`
	Reranker RerankerRAGConfig `yaml:"reranker"`
}

type VectorRAGConfig struct {
	Enabled          bool   `yaml:"enabled"`
	EmbeddingModelID string `yaml:"embedding_model_id"`
	QdrantBaseURL    string `yaml:"qdrant_base_url"`
	QdrantBaseURLEnv string `yaml:"qdrant_base_url_env"`
	QdrantCollection string `yaml:"qdrant_collection"`
	QdrantAPIKeyEnv  string `yaml:"qdrant_api_key_env"`
}

type RerankerRAGConfig struct {
	Enabled bool   `yaml:"enabled"`
	ModelID string `yaml:"model_id"`
}

type BrowserConfig struct {
	Enabled       bool                `yaml:"enabled"`
	Runtime       string              `yaml:"runtime"`
	ScreenshotDir string              `yaml:"screenshot_dir"`
	ProfileReuse  BrowserProfileReuse `yaml:"profile_reuse"`
}

type MCPConfig struct {
	Servers map[string]MCPServerConfig `yaml:"servers"`
}

type MCPServerConfig struct {
	Enabled        bool     `yaml:"enabled"`
	Command        string   `yaml:"command"`
	Args           []string `yaml:"args"`
	Tools          []string `yaml:"tools"`
	Timeout        Duration `yaml:"timeout"`
	TrustLevel     string   `yaml:"trust_level"`
	PrivacyClasses []string `yaml:"privacy_classes"`
}

type ToolsConfig struct {
	Allowlist []ToolConfig `yaml:"allowlist"`
}

type ToolConfig struct {
	ID              string   `yaml:"id"`
	Enabled         bool     `yaml:"enabled"`
	Program         string   `yaml:"program"`
	Args            []string `yaml:"args"`
	Timeout         Duration `yaml:"timeout"`
	SideEffectLevel string   `yaml:"side_effect_level"`
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

type SecurityConfig struct {
	API     APISecurityConfig     `yaml:"api"`
	Network NetworkSecurityConfig `yaml:"network"`
}

type APISecurityConfig struct {
	AuthTokenEnv       string   `yaml:"auth_token_env"`
	AllowedOrigins     []string `yaml:"allowed_origins"`
	MaxBodyBytes       int64    `yaml:"max_body_bytes"`
	RateLimitPerMinute int      `yaml:"rate_limit_per_minute"`
}

type NetworkSecurityConfig struct {
	AllowedDomains       []string `yaml:"allowed_domains"`
	AllowPrivateNetworks bool     `yaml:"allow_private_networks"`
	AllowedSchemes       []string `yaml:"allowed_schemes"`
}

type ReliabilityConfig struct {
	Resources ResourceLimitsConfig `yaml:"resources"`
	Disk      DiskPressureConfig   `yaml:"disk"`
}

type ResourceLimitsConfig struct {
	MaxConcurrentWorkflows int   `yaml:"max_concurrent_workflows"`
	MaxModelCalls          int   `yaml:"max_model_calls"`
	MaxBrowserSessions     int   `yaml:"max_browser_sessions"`
	MaxCodingAgents        int   `yaml:"max_coding_agents"`
	MaxMCPCalls            int   `yaml:"max_mcp_calls"`
	MaxOpenFiles           int   `yaml:"max_open_files"`
	MaxTemporaryDiskBytes  int64 `yaml:"max_temporary_disk_bytes"`
}

type DiskPressureConfig struct {
	Paths             []string `yaml:"paths"`
	SoftLimitBytes    int64    `yaml:"soft_limit_bytes"`
	HardLimitBytes    int64    `yaml:"hard_limit_bytes"`
	CleanupTempOnSoft bool     `yaml:"cleanup_temp_on_soft"`
}

type Duration struct {
	time.Duration
}
