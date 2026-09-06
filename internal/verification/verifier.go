package verification

import "strings"

type Verifier struct {
	Policy Policy
}

func (v Verifier) Verify(claims []Claim, evidence []Evidence) Report {
	policy := v.Policy
	if policy.MinCoverage == 0 && policy.MinClaimConfidence == 0 && !policy.AllowConflicts && !policy.RequireEvidence {
		policy = DefaultPolicy()
	}
	evidenceByID := make(map[string]Evidence, len(evidence))
	for _, item := range evidence {
		evidenceByID[item.ID] = item
	}
	results := make([]ClaimResult, 0, len(claims))
	var coverageSum float64
	var conflicts int
	for _, claim := range claims {
		result := verifyClaim(claim, evidenceByID, policy)
		coverageSum += result.Coverage
		conflicts += len(result.ConflictEvidenceIDs)
		results = append(results, result)
	}
	report := Report{
		Results:       results,
		ConflictCount: conflicts,
	}
	if len(results) > 0 {
		report.Coverage = coverageSum / float64(len(results))
	}
	report.PassesPolicy, report.FailureReasons = evaluatePolicy(report, policy)
	return report
}

func verifyClaim(claim Claim, evidenceByID map[string]Evidence, policy Policy) ClaimResult {
	result := ClaimResult{Claim: claim}
	if claim.Confidence < policy.MinClaimConfidence {
		result.Status = ClaimLowConfidence
		return result
	}
	seen := make(map[string]bool)
	for _, id := range claim.EvidenceIDs {
		item, ok := evidenceByID[id]
		if !ok {
			continue
		}
		if evidenceSupportsClaim(item, claim.Text) {
			result.CoveredEvidenceIDs = append(result.CoveredEvidenceIDs, id)
			seen[id] = true
		}
		if evidenceContradictsClaim(item, claim.Text) {
			result.ConflictEvidenceIDs = append(result.ConflictEvidenceIDs, id)
		}
	}
	if len(claim.EvidenceIDs) > 0 {
		result.Coverage = float64(len(seen)) / float64(len(claim.EvidenceIDs))
	}
	switch {
	case len(result.ConflictEvidenceIDs) > 0:
		result.Status = ClaimConflicted
	case policy.RequireEvidence && result.Coverage < policy.MinCoverage:
		result.Status = ClaimUnverified
	default:
		result.Status = ClaimSupported
	}
	return result
}

func evidenceSupportsClaim(evidence Evidence, claimText string) bool {
	return matchesAny(evidence.Supports, claimText) || containsFold(evidence.Text, claimText)
}

func evidenceContradictsClaim(evidence Evidence, claimText string) bool {
	return matchesAny(evidence.Contradicts, claimText)
}

func matchesAny(values []string, claimText string) bool {
	for _, value := range values {
		if containsFold(claimText, value) || containsFold(value, claimText) {
			return true
		}
	}
	return false
}

func containsFold(haystack, needle string) bool {
	haystack = strings.ToLower(strings.TrimSpace(haystack))
	needle = strings.ToLower(strings.TrimSpace(needle))
	return needle != "" && strings.Contains(haystack, needle)
}

func evaluatePolicy(report Report, policy Policy) (bool, []string) {
	var reasons []string
	if report.Coverage < policy.MinCoverage {
		reasons = append(reasons, "coverage below policy")
	}
	if !policy.AllowConflicts && report.ConflictCount > 0 {
		reasons = append(reasons, "conflicts detected")
	}
	for _, result := range report.Results {
		if result.Status == ClaimLowConfidence {
			reasons = append(reasons, "claim confidence below policy")
			break
		}
		if policy.RequireEvidence && result.Status == ClaimUnverified {
			reasons = append(reasons, "claim evidence missing")
			break
		}
	}
	return len(reasons) == 0, reasons
}
