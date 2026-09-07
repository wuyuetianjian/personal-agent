package runtime

import (
	"time"

	"agent/internal/model"
	"agent/internal/verification"
)

type RouteType string

const (
	RouteDirect  RouteType = "direct"
	RouteMemory  RouteType = "memory"
	RouteRAG     RouteType = "rag"
	RouteTool    RouteType = "tool"
	RouteBrowser RouteType = "browser"
	RouteAgent   RouteType = "agent"
	RouteMixed   RouteType = "mixed"
)

type SourceType string

const (
	SourceUser    SourceType = "user"
	SourceRAG     SourceType = "rag"
	SourceMemory  SourceType = "memory"
	SourceBrowser SourceType = "browser"
	SourceTool    SourceType = "tool"
	SourceModel   SourceType = "model"
	SourceSystem  SourceType = "system"
)

type Evidence struct {
	ID           string
	TaskID       string
	NodeID       string
	Claim        string
	SourceType   SourceType
	SourceID     string
	Content      string
	Score        float64
	Trust        float64
	PrivacyClass string
	CreatedAt    time.Time
}

type RunRequest struct {
	TaskID        string
	Input         string
	PrivacyClass  string
	LeaderModelID string
	MaxIterations int
	MaxTokens     int
	MaxCostUSD    float64
}

type UsageSummary struct {
	InputTokens      int
	OutputTokens     int
	RemoteTokens     int
	EstimatedCostUSD float64
}

type RunResult struct {
	TaskID         string
	Answer         string
	Confidence     float64
	EvidenceIDs    []string
	Usage          UsageSummary
	EarlyStopped   bool
	Verification   verification.Report
	RemoteCalls    int
	LocalRouteType RouteType
}

type ChatResult struct {
	Answer string
	Usage  model.Usage
}
