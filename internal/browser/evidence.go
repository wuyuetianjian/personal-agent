package browser

import (
	"context"
	"sync"
	"time"
)

type EvidenceCategory string

const (
	EvidenceNavigationRequested EvidenceCategory = "browser.navigation.requested"
	EvidenceNavigationCompleted EvidenceCategory = "browser.navigation.completed"
	EvidenceReadDOM             EvidenceCategory = "browser.read.dom"
	EvidenceReadAccessibility   EvidenceCategory = "browser.read.accessibility"
	EvidenceScreenshotCaptured  EvidenceCategory = "browser.screenshot.captured"
	EvidenceElementLocated      EvidenceCategory = "browser.element.located"
	EvidenceActionProposed      EvidenceCategory = "browser.action.proposed"
	EvidenceActionApproved      EvidenceCategory = "browser.action.approved"
	EvidenceActionDenied        EvidenceCategory = "browser.action.denied"
	EvidenceActionExecuted      EvidenceCategory = "browser.action.executed"
	EvidenceActionFailed        EvidenceCategory = "browser.action.failed"
	EvidenceSessionAttached     EvidenceCategory = "browser.session.attached"
	EvidenceSessionExpired      EvidenceCategory = "browser.session.expired"
	EvidenceCancelled           EvidenceCategory = "browser.cancelled"
)

type EvidenceEvent struct {
	Category    EvidenceCategory
	TaskID      string
	AgentID     string
	SessionID   string
	Domain      string
	ActionType  ActionType
	Decision    PermissionDecision
	Target      Target
	Observation Observation
	Cancelled   bool
	RecordedAt  time.Time
}

type EvidenceBuilder struct {
	Privacy PrivacyGateway
}

func (b EvidenceBuilder) Build(category EvidenceCategory, taskID string, agentID string, result Result, cancelled bool) EvidenceEvent {
	return EvidenceEvent{
		Category:   category,
		TaskID:     taskID,
		AgentID:    agentID,
		SessionID:  result.Action.SessionID,
		Domain:     b.Privacy.Redact(result.Action.Domain),
		ActionType: result.Action.Type,
		Decision:   result.Decision,
		Target: Target{
			Role:        b.Privacy.Redact(result.Action.Target.Role),
			Text:        b.Privacy.Redact(result.Action.Target.Text),
			Selector:    b.Privacy.Redact(result.Action.Target.Selector),
			Description: b.Privacy.Redact(result.Action.Target.Description),
			X:           result.Action.Target.X,
			Y:           result.Action.Target.Y,
		},
		Observation: Observation{
			URL:          b.Privacy.Redact(result.Observation.URL),
			Title:        b.Privacy.Redact(result.Observation.Title),
			VisibleText:  b.Privacy.Redact(result.Observation.VisibleText),
			DOMSummary:   b.Privacy.Redact(result.Observation.DOMSummary),
			A11ySummary:  b.Privacy.Redact(result.Observation.A11ySummary),
			ScreenshotID: b.Privacy.Redact(result.Observation.ScreenshotID),
		},
		Cancelled:  cancelled,
		RecordedAt: result.RecordedAt,
	}
}

type InMemoryBrowserEvidenceBus struct {
	mu     sync.Mutex
	events []EvidenceEvent
}

func (b *InMemoryBrowserEvidenceBus) PublishBrowserEvent(ctx context.Context, event EvidenceEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.RecordedAt.IsZero() {
		event.RecordedAt = time.Now().UTC()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, event)
	return nil
}

func (b *InMemoryBrowserEvidenceBus) Events() []EvidenceEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	events := make([]EvidenceEvent, len(b.events))
	copy(events, b.events)
	return events
}
