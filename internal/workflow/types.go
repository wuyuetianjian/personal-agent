package workflow

import "time"

type Status string

const (
	StatusPending         Status = "pending"
	StatusRunning         Status = "running"
	StatusWaitingApproval Status = "waiting_approval"
	StatusPaused          Status = "paused"
	StatusRetrying        Status = "retrying"
	StatusCompleted       Status = "completed"
	StatusFailed          Status = "failed"
	StatusCancelled       Status = "cancelled"
)

type NodeStatus string

const (
	NodePending         NodeStatus = "pending"
	NodeReady           NodeStatus = "ready"
	NodeRunning         NodeStatus = "running"
	NodeWaitingApproval NodeStatus = "waiting_approval"
	NodeCompleted       NodeStatus = "completed"
	NodeFailed          NodeStatus = "failed"
	NodeSkipped         NodeStatus = "skipped"
	NodeCancelled       NodeStatus = "cancelled"
)

type Run struct {
	ID           string
	TaskID       string
	ProjectID    string
	SkillID      string
	SkillVersion string
	Status       Status
	InputJSON    string
	ResultJSON   string
	StartedAt    time.Time
	UpdatedAt    time.Time
	CompletedAt  *time.Time
}

type Node struct {
	WorkflowID     string
	NodeID         string
	CapabilityID   string
	Role           string
	Status         NodeStatus
	Attempt        int
	IdempotencyKey string
	SideEffect     bool
	StartedAt      *time.Time
	CompletedAt    *time.Time
}

type Checkpoint struct {
	WorkflowID  string
	NodeID      string
	Status      NodeStatus
	EvidenceIDs []string
	ResultRef   string
	UsageJSON   string
	CreatedAt   time.Time
}
