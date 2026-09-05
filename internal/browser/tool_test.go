package browser

import (
	"context"
	"testing"
	"time"
)

type fakeRuntime struct {
	called bool
}

func (r *fakeRuntime) Execute(context.Context, Action) (Observation, error) {
	r.called = true
	return Observation{Title: "Example"}, nil
}

func TestGovernedToolDoesNotExecuteWithoutAllowedDecision(t *testing.T) {
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

	runtime := &fakeRuntime{}
	tool := GovernedTool{
		Sessions: manager,
		Classifier: PermissionClassifier{Policy: PermissionPolicy{
			AutoAllowRead:            true,
			AutoAllowNavigation:      true,
			RequireAllowlistedDomain: true,
		}},
		Runtime: runtime,
	}

	result, err := tool.Execute(context.Background(), Action{
		Type:      ActionSendMessage,
		SessionID: "session-1",
		URL:       "https://example.com/messages",
		Domain:    "example.com",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Decision != DecisionRequiresConfirmation {
		t.Fatalf("Decision = %q, want %q", result.Decision, DecisionRequiresConfirmation)
	}
	if runtime.called {
		t.Fatal("runtime was called before confirmation")
	}
}

func TestGovernedToolExecutesAllowedRead(t *testing.T) {
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

	runtime := &fakeRuntime{}
	tool := GovernedTool{
		Sessions: manager,
		Classifier: PermissionClassifier{Policy: PermissionPolicy{
			AutoAllowRead:            true,
			RequireAllowlistedDomain: true,
		}},
		Runtime: runtime,
	}

	result, err := tool.Execute(context.Background(), Action{
		Type:      ActionReadTitle,
		SessionID: "session-1",
		URL:       "https://example.com",
		Domain:    "example.com",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Decision != DecisionAllowed {
		t.Fatalf("Decision = %q, want %q", result.Decision, DecisionAllowed)
	}
	if !runtime.called {
		t.Fatal("runtime was not called for allowed read")
	}
}
