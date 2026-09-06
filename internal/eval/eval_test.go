package eval

import "testing"

func TestP13GoldenCasesCoverReleaseAreas(t *testing.T) {
	cases := P13GoldenCases()
	if len(cases) < 13 {
		t.Fatalf("got %d cases, want release-gate coverage", len(cases))
	}
	seen := map[string]bool{}
	for _, item := range cases {
		seen[item.Area] = true
	}
	for _, area := range []string{"local_qa", "memory", "rag", "browser", "mcp", "coding", "trigger"} {
		if !seen[area] {
			t.Fatalf("missing area %s", area)
		}
	}
}

func TestEvaluateQualityReturnsFailures(t *testing.T) {
	failures := EvaluateQuality(Metrics{
		VerificationPassRate: 0.5,
		RetrievalQuality:     0.9,
		RoutingCorrectness:   0.95,
		PrivacyRegression:    1,
		WorkflowRecovery:     0.95,
		NotificationNoise:    0.2,
		CostCeiling:          2,
	}, DefaultThresholds())
	if len(failures) != 3 {
		t.Fatalf("failures = %v, want three failures", failures)
	}
}
