package agent

type Result struct {
	TaskID        string
	NodeID        string
	Role          Role
	Text          string
	Claims        []Claim
	EvidenceIDs   []string
	Usage         Usage
	ErrorCategory ErrorCategory
	ErrorMessage  string
}

type Claim struct {
	Text        string
	Confidence  float64
	EvidenceIDs []string
}

type Usage struct {
	InputTokens      int
	OutputTokens     int
	BillableUnits    float64
	EstimatedCostUSD float64
}
