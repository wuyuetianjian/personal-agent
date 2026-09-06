package cost

import "time"

type Pricing struct {
	InputPer1M  float64
	OutputPer1M float64
}

type Usage struct {
	TaskID           string
	NodeID           string
	AgentRole        string
	ProviderID       string
	ModelID          string
	Operation        string
	InputTokens      int
	OutputTokens     int
	BillableUnits    float64
	EstimatedCostUSD float64
	CreatedAt        time.Time
}

type Budget struct {
	MaxInputTokens      int
	MaxOutputTokens     int
	SoftLimitUSD        float64
	HardLimitUSD        float64
	CurrentCostUSD      float64
	CurrentInputTokens  int
	CurrentOutputTokens int
}

type LimitStatus string

const (
	LimitOK      LimitStatus = "ok"
	LimitSoftHit LimitStatus = "soft_limit_hit"
	LimitHardHit LimitStatus = "hard_limit_hit"
)

type LimitResult struct {
	Status  LimitStatus
	Reasons []string
}
