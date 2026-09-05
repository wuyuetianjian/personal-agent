package browser

import (
	"strings"
	"testing"
	"time"
)

func TestEvidenceBuilderRedactsTargetAndObservation(t *testing.T) {
	builder := EvidenceBuilder{Privacy: PrivacyGateway{}}
	event := builder.Build(EvidenceActionExecuted, "task-1", "agent-1", Result{
		Action: Action{
			Type:      ActionReadDOM,
			SessionID: "session-1",
			Domain:    "app.example.com",
			Target: Target{
				Text:        "alice@example.com",
				Description: "token=secret123",
			},
		},
		Observation: Observation{
			URL:         "https://app.example.com?token=secret123",
			VisibleText: "password=hunter2",
			DOMSummary:  `<input name="csrf" value="raw">`,
		},
		Decision:   DecisionAllowed,
		RecordedAt: time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC),
	}, false)

	combined := strings.Join([]string{
		event.Target.Text,
		event.Target.Description,
		event.Observation.URL,
		event.Observation.VisibleText,
		event.Observation.DOMSummary,
	}, " ")

	for _, value := range []string{"alice@example.com", "secret123", "hunter2", "raw"} {
		if strings.Contains(combined, value) {
			t.Fatalf("Evidence leaked %q in %q", value, combined)
		}
	}
}
