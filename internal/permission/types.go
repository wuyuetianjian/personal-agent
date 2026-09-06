package permission

import "time"

type Action string

const (
	ActionFileSystem         Action = "file_system"
	ActionNetwork            Action = "network"
	ActionBrowserRead        Action = "browser_read"
	ActionBrowserWrite       Action = "browser_write"
	ActionBrowserHighRisk    Action = "browser_high_risk"
	ActionMemoryWrite        Action = "memory_write"
	ActionPublicModelCall    Action = "public_model_call"
	ActionCredentialAccess   Action = "credential_access"
	ActionExternalSideEffect Action = "external_side_effect"
)

type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

type Decision string

const (
	DecisionAllow   Decision = "allow"
	DecisionDeny    Decision = "deny"
	DecisionConfirm Decision = "confirm"
)

type Request struct {
	ID             string
	TaskID         string
	NodeID         string
	Action         Action
	Target         string
	Risk           RiskLevel
	EvidenceIDs    []string
	ProposedEffect string
	RequestedAt    time.Time
}

type Result struct {
	Request        Request
	Decision       Decision
	Reason         string
	ConfirmationID string
	DecidedAt      time.Time
}

type AuditRecord struct {
	ID             string
	TaskID         string
	NodeID         string
	Action         Action
	Target         string
	Decision       Decision
	Risk           RiskLevel
	Reason         string
	ConfirmationID string
	EvidenceIDs    []string
	ProposedEffect string
	CreatedAt      time.Time
}
