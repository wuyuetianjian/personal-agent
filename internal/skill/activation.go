package skill

import "errors"

var ErrEvalGateFailed = errors.New("skill eval gate failed")

type EvalResult struct {
	Passed bool
	Score  float64
	Reason string
}

func ActivateWithEval(registry *Registry, id string, version string, eval EvalResult) error {
	if !eval.Passed {
		return ErrEvalGateFailed
	}
	return registry.Activate(id, version)
}

func CandidateFromIntent(intent string, capabilities []string) Manifest {
	return Manifest{
		ID:          "draft-" + checksum(intent),
		Version:     "0.1.0",
		Name:        intent,
		Description: "Draft skill candidate for " + intent,
		Status:      StatusDraft,
		Requires: Requirements{
			Capabilities: append([]string(nil), capabilities...),
		},
		Privacy: PrivacyPolicy{MaxExternalTrust: "local_private"},
		Budget:  Budget{MaxIterations: 3, MaxToolCalls: len(capabilities)},
	}
}
