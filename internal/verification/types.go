package verification

type Claim struct {
	ID          string
	Text        string
	Confidence  float64
	EvidenceIDs []string
}

type Evidence struct {
	ID          string
	Text        string
	Supports    []string
	Contradicts []string
}

type ClaimStatus string

const (
	ClaimSupported     ClaimStatus = "supported"
	ClaimUnverified    ClaimStatus = "unverified"
	ClaimConflicted    ClaimStatus = "conflicted"
	ClaimLowConfidence ClaimStatus = "low_confidence"
)

type ClaimResult struct {
	Claim               Claim
	Status              ClaimStatus
	CoveredEvidenceIDs  []string
	ConflictEvidenceIDs []string
	Coverage            float64
}

type Report struct {
	Results        []ClaimResult
	Coverage       float64
	ConflictCount  int
	PassesPolicy   bool
	FailureReasons []string
}

type Policy struct {
	MinClaimConfidence float64
	MinCoverage        float64
	AllowConflicts     bool
	RequireEvidence    bool
}

func DefaultPolicy() Policy {
	return Policy{
		MinClaimConfidence: 0.6,
		MinCoverage:        1,
		AllowConflicts:     false,
		RequireEvidence:    true,
	}
}
