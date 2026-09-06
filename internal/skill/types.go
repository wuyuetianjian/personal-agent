package skill

import "time"

type Status string

const (
	StatusDraft      Status = "draft"
	StatusActive     Status = "active"
	StatusDeprecated Status = "deprecated"
	StatusDisabled   Status = "disabled"
)

type Manifest struct {
	ID           string             `yaml:"id" json:"id"`
	Version      string             `yaml:"version" json:"version"`
	Name         string             `yaml:"name" json:"name"`
	Description  string             `yaml:"description" json:"description"`
	Status       Status             `yaml:"status" json:"status"`
	Inputs       map[string]Field   `yaml:"inputs" json:"inputs"`
	Requires     Requirements       `yaml:"requires" json:"requires"`
	Permissions  PermissionPolicy   `yaml:"permissions" json:"permissions"`
	Privacy      PrivacyPolicy      `yaml:"privacy" json:"privacy"`
	Budget       Budget             `yaml:"budget" json:"budget"`
	Verification VerificationPolicy `yaml:"verification" json:"verification"`
	Workflow     Workflow           `yaml:"workflow" json:"workflow"`
	CreatedAt    time.Time          `yaml:"-" json:"created_at,omitempty"`
}

type Field struct {
	Type string `yaml:"type" json:"type"`
}
type Requirements struct {
	Capabilities []string `yaml:"capabilities" json:"capabilities"`
}
type PermissionPolicy struct {
	MaxLevel string `yaml:"max_level" json:"max_level"`
}
type PrivacyPolicy struct {
	MaxExternalTrust string `yaml:"max_external_trust" json:"max_external_trust"`
}
type Budget struct {
	MaxIterations int `yaml:"max_iterations" json:"max_iterations"`
	MaxToolCalls  int `yaml:"max_tool_calls" json:"max_tool_calls"`
}
type VerificationPolicy struct {
	MinCoverage    float64 `yaml:"min_coverage" json:"min_coverage"`
	AllowConflicts bool    `yaml:"allow_conflicts" json:"allow_conflicts"`
}
type Workflow struct {
	Nodes []Node `yaml:"nodes" json:"nodes"`
}
type Node struct {
	ID         string   `yaml:"id" json:"id"`
	Capability string   `yaml:"capability" json:"capability"`
	Role       string   `yaml:"role" json:"role"`
	DependsOn  []string `yaml:"depends_on" json:"depends_on"`
}

type MatchRequest struct {
	Intent               string
	RequiredCapabilities []string
	PrivacyClass         string
	AllowedSkillIDs      []string
}
type Match struct {
	Manifest Manifest
	Score    float64
	Reason   string
}
