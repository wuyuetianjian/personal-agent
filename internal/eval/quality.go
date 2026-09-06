package eval

type Metrics struct {
	VerificationPassRate float64
	RetrievalQuality     float64
	RoutingCorrectness   float64
	PrivacyRegression    float64
	WorkflowRecovery     float64
	NotificationNoise    float64
	CostCeiling          float64
}

type Thresholds struct {
	VerificationPassRate float64
	RetrievalQuality     float64
	RoutingCorrectness   float64
	PrivacyRegression    float64
	WorkflowRecovery     float64
	NotificationNoiseMax float64
	CostCeilingMax       float64
}

func DefaultThresholds() Thresholds {
	return Thresholds{
		VerificationPassRate: 0.85,
		RetrievalQuality:     0.8,
		RoutingCorrectness:   0.9,
		PrivacyRegression:    1,
		WorkflowRecovery:     0.9,
		NotificationNoiseMax: 0.1,
		CostCeilingMax:       1,
	}
}

func EvaluateQuality(metrics Metrics, thresholds Thresholds) []string {
	var failures []string
	if metrics.VerificationPassRate < thresholds.VerificationPassRate {
		failures = append(failures, "verification_pass_rate")
	}
	if metrics.RetrievalQuality < thresholds.RetrievalQuality {
		failures = append(failures, "retrieval_quality")
	}
	if metrics.RoutingCorrectness < thresholds.RoutingCorrectness {
		failures = append(failures, "routing_correctness")
	}
	if metrics.PrivacyRegression < thresholds.PrivacyRegression {
		failures = append(failures, "privacy_regression")
	}
	if metrics.WorkflowRecovery < thresholds.WorkflowRecovery {
		failures = append(failures, "workflow_recovery")
	}
	if metrics.NotificationNoise > thresholds.NotificationNoiseMax {
		failures = append(failures, "notification_noise")
	}
	if metrics.CostCeiling > thresholds.CostCeilingMax {
		failures = append(failures, "cost_ceiling")
	}
	return failures
}
