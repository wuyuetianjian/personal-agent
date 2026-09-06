package browser

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type mockRuntime struct {
	locateErr error
	clicked   Target
	observed  Observation
}

func (r *mockRuntime) Navigate(context.Context, string) (Observation, error) {
	return r.observed, nil
}
func (r *mockRuntime) Observe(context.Context) (Observation, error) {
	return r.observed, nil
}
func (r *mockRuntime) Locate(context.Context, Target) (LocatedElement, error) {
	if r.locateErr != nil {
		return LocatedElement{}, r.locateErr
	}
	return LocatedElement{Selector: "#ok", Text: "ok"}, nil
}
func (r *mockRuntime) Click(_ context.Context, target Target) (Observation, error) {
	r.clicked = target
	return r.observed, nil
}
func (r *mockRuntime) MoveMouse(context.Context, int, int) (Observation, error) {
	return r.observed, nil
}
func (r *mockRuntime) MouseWheel(context.Context, int, int) (Observation, error) {
	return r.observed, nil
}
func (r *mockRuntime) TypeText(context.Context, Target, string) (Observation, error) {
	return r.observed, nil
}
func (r *mockRuntime) KeyboardShortcut(context.Context, []string) (Observation, error) {
	return r.observed, nil
}
func (r *mockRuntime) AccessibilityTree(context.Context) (Observation, error) {
	return r.observed, nil
}
func (r *mockRuntime) Screenshot(context.Context) (Observation, error) {
	return r.observed, nil
}

func TestRuntimeActionAdapterCoordinateFallback(t *testing.T) {
	runtime := &mockRuntime{
		locateErr: ErrElementNotFound,
		observed:  Observation{Title: "after click"},
	}
	adapter := RuntimeActionAdapter{Runtime: runtime}

	got, err := adapter.Execute(context.Background(), Action{
		Type:   ActionClick,
		Mode:   ControlModeSemantic,
		Target: Target{Text: "Missing", X: 12, Y: 34},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got.Title != "after click" {
		t.Fatalf("Title = %q, want after click", got.Title)
	}
	if runtime.clicked.X != 12 || runtime.clicked.Y != 34 {
		t.Fatalf("fallback target = %+v, want coordinates", runtime.clicked)
	}
}

func TestRuntimeActionAdapterCoordinateFallbackRequiresCoordinates(t *testing.T) {
	adapter := RuntimeActionAdapter{Runtime: &mockRuntime{locateErr: ErrElementNotFound}}

	_, err := adapter.Execute(context.Background(), Action{
		Type:   ActionClick,
		Mode:   ControlModeSemantic,
		Target: Target{Text: "Missing"},
	})
	if !errors.Is(err, ErrElementNotFound) {
		t.Fatalf("Execute() error = %v, want %v", err, ErrElementNotFound)
	}
}

func TestGovernedToolPublishesEvidenceAndRequiresConfirmation(t *testing.T) {
	now := time.Now().UTC()
	manager := NewInMemorySessionManager()
	if _, err := manager.Create(Session{
		ID:              "session-1",
		Domains:         []string{"example.com"},
		ExpiresAt:       now.Add(time.Hour),
		ReusePolicy:     ReuseWhenAllowed,
		IsolationPolicy: IsolationPerDomain,
		StoragePolicy:   StorageLocalOnly,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	confirmations := NewInMemoryConfirmationStore()
	bus := &InMemoryBrowserEvidenceBus{}
	tool := GovernedTool{
		Sessions: manager,
		Classifier: PermissionClassifier{Policy: PermissionPolicy{
			AutoAllowRead:               true,
			AutoAllowNavigation:         true,
			AutoAllowLowRiskInteraction: true,
			RequireAllowlistedDomain:    true,
		}},
		Runtime: RuntimeActionAdapter{
			Runtime:      &mockRuntime{observed: Observation{Title: "sent"}},
			Confirmation: confirmations,
		},
		Evidence:    EvidenceBuilder{Privacy: PrivacyGateway{}},
		EvidenceBus: bus,
	}

	action := Action{
		Type:           ActionSendMessage,
		TaskID:         "task-1",
		AgentID:        "agent-1",
		SessionID:      "session-1",
		URL:            "https://example.com/messages",
		Domain:         "example.com",
		ConfirmationID: "confirm-1",
		Target:         Target{Text: "alice@example.com"},
	}

	result, err := tool.Execute(context.Background(), action)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Decision != DecisionRequiresConfirmation {
		t.Fatalf("Decision = %q, want confirmation", result.Decision)
	}
	if len(bus.Events()) != 2 {
		t.Fatalf("events = %d, want proposed and denied", len(bus.Events()))
	}

	confirmations.Approve("confirm-1")
	result, err = tool.Execute(context.Background(), action)
	if err != nil {
		t.Fatalf("Execute() approved error = %v", err)
	}
	if result.Decision != DecisionAllowed {
		t.Fatalf("approved Decision = %q, want allowed", result.Decision)
	}

	events := bus.Events()
	if events[len(events)-1].Category != EvidenceActionExecuted {
		t.Fatalf("last event = %q, want executed", events[len(events)-1].Category)
	}
	if strings.Contains(events[len(events)-1].Target.Text, "alice@example.com") {
		t.Fatalf("evidence leaked target text: %+v", events[len(events)-1].Target)
	}
}

func TestGovernedToolDomainDenyPublishesDeniedEvidence(t *testing.T) {
	now := time.Now().UTC()
	manager := NewInMemorySessionManager()
	if _, err := manager.Create(Session{
		ID:              "session-1",
		Domains:         []string{"example.com"},
		ExpiresAt:       now.Add(time.Hour),
		ReusePolicy:     ReuseWhenAllowed,
		IsolationPolicy: IsolationPerDomain,
		StoragePolicy:   StorageLocalOnly,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	bus := &InMemoryBrowserEvidenceBus{}
	tool := GovernedTool{
		Sessions: manager,
		Classifier: PermissionClassifier{Policy: PermissionPolicy{
			AutoAllowNavigation:      true,
			RequireAllowlistedDomain: true,
		}},
		Runtime:     RuntimeActionAdapter{Runtime: &mockRuntime{}},
		Evidence:    EvidenceBuilder{Privacy: PrivacyGateway{}},
		EvidenceBus: bus,
	}

	result, err := tool.Execute(context.Background(), Action{
		Type:      ActionNavigate,
		TaskID:    "task-1",
		AgentID:   "agent-1",
		SessionID: "session-1",
		URL:       "https://outside.test",
		Domain:    "outside.test",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Decision != DecisionDenied {
		t.Fatalf("Decision = %q, want denied", result.Decision)
	}
	events := bus.Events()
	if len(events) != 2 || events[1].Category != EvidenceActionDenied {
		t.Fatalf("events = %+v, want denied evidence", events)
	}
}
