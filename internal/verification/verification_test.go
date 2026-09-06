package verification

import "testing"

func TestExtractClaimsWithEvidenceMarkers(t *testing.T) {
	claims := ExtractClaims("- The page title is Home [evidence:ev-1, ev-2]\nThe task completed")
	if len(claims) != 2 {
		t.Fatalf("len(claims) = %d, want 2", len(claims))
	}
	if claims[0].Text != "The page title is Home" {
		t.Fatalf("claim text = %q", claims[0].Text)
	}
	if got := claims[0].EvidenceIDs; len(got) != 2 || got[0] != "ev-1" || got[1] != "ev-2" {
		t.Fatalf("evidence IDs = %#v", got)
	}
}

func TestVerifyClaimEvidenceCoverage(t *testing.T) {
	claims := []Claim{{ID: "c1", Text: "The page title is Home", Confidence: 0.9, EvidenceIDs: []string{"ev-1"}}}
	evidence := []Evidence{{ID: "ev-1", Supports: []string{"page title is Home"}}}
	report := Verifier{Policy: DefaultPolicy()}.Verify(claims, evidence)
	if !report.PassesPolicy || report.Coverage != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.Results[0].Status != ClaimSupported {
		t.Fatalf("status = %s", report.Results[0].Status)
	}
}

func TestVerifyDetectsConflict(t *testing.T) {
	claims := []Claim{{ID: "c1", Text: "The account was deleted", Confidence: 0.9, EvidenceIDs: []string{"ev-1"}}}
	evidence := []Evidence{{ID: "ev-1", Contradicts: []string{"account was deleted"}}}
	report := Verifier{Policy: DefaultPolicy()}.Verify(claims, evidence)
	if report.PassesPolicy {
		t.Fatal("PassesPolicy = true, want false")
	}
	if report.ConflictCount != 1 || report.Results[0].Status != ClaimConflicted {
		t.Fatalf("report = %+v", report)
	}
}

func TestVerifyRejectsLowConfidence(t *testing.T) {
	claims := []Claim{{ID: "c1", Text: "Low confidence claim", Confidence: 0.2, EvidenceIDs: []string{"ev-1"}}}
	evidence := []Evidence{{ID: "ev-1", Supports: []string{"Low confidence claim"}}}
	report := Verifier{Policy: DefaultPolicy()}.Verify(claims, evidence)
	if report.PassesPolicy || report.Results[0].Status != ClaimLowConfidence {
		t.Fatalf("report = %+v", report)
	}
}
