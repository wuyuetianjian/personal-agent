package capability

import "context"

type Kind string

const (
	KindRetrieval    Kind = "retrieval"
	KindMemory       Kind = "memory"
	KindReasoning    Kind = "reasoning"
	KindBrowser      Kind = "browser"
	KindTool         Kind = "tool"
	KindCodingAgent  Kind = "coding_agent"
	KindMCPTool      Kind = "mcp_tool"
	KindModel        Kind = "model"
	KindSkill        Kind = "skill"
	KindVerification Kind = "verification"
)

type Health string

const (
	HealthUnknown     Health = "unknown"
	HealthHealthy     Health = "healthy"
	HealthDegraded    Health = "degraded"
	HealthUnavailable Health = "unavailable"
)

type Capability struct {
	ID              string   `json:"id" yaml:"id"`
	Kind            Kind     `json:"kind" yaml:"kind"`
	Description     string   `json:"description" yaml:"description"`
	TrustLevel      string   `json:"trust_level" yaml:"trust_level"`
	SideEffectLevel string   `json:"side_effect_level" yaml:"side_effect_level"`
	PrivacyClasses  []string `json:"privacy_classes" yaml:"privacy_classes"`
	CostClass       string   `json:"cost_class" yaml:"cost_class"`
	LatencyClass    string   `json:"latency_class" yaml:"latency_class"`
	Tags            []string `json:"tags" yaml:"tags"`
	InputSchema     []byte   `json:"input_schema,omitempty" yaml:"input_schema,omitempty"`
	OutputSchema    []byte   `json:"output_schema,omitempty" yaml:"output_schema,omitempty"`
	Enabled         bool     `json:"enabled" yaml:"enabled"`
	Health          Health   `json:"health" yaml:"health"`
}

type ResolveRequest struct {
	TaskID        string
	ProjectID     string
	Intent        string
	RequiredKind  Kind
	PrivacyClass  string
	RequiredTags  []string
	MaxTrustLevel string
	MaxCostClass  string
	AllowedIDs    []string
	DeniedIDs     []string
	MaxSideEffect string
}

type Candidate struct {
	Capability Capability
	Score      float64
	Reason     string
}

type RuntimeMetric struct {
	Successes          int
	Failures           int
	VerificationPasses int
	Retries            int
	Cancels            int
	LatencyMillis      int
	EstimatedCostUSD   float64
}

type Resolver interface {
	Resolve(context.Context, ResolveRequest) ([]Candidate, error)
}
