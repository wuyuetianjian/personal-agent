package project

import (
	"encoding/json"
	"errors"
)

const (
	ExecutionLegacyPolicy = "legacy_policy"
	ExecutionMetadataOnly = "metadata_only"
)

var ErrInvalidExecutionClass = errors.New("project execution classification missing or invalid")
var ErrProjectWriteDenied = errors.New("project policy save denied; imported or metadata-only projects cannot provision execution policy")

type Project struct {
	ExecutionClass string `json:"execution_class"`
	// JSON is a portable/display representation, never proof of policy authority.
	imported            bool
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	PrivacyClass        string   `json:"privacy_class"`
	RepositoryRefs      []string `json:"repository_refs"`
	KnowledgeScopes     []string `json:"knowledge_scopes"`
	MemoryScope         string   `json:"memory_scope"`
	AllowedSkills       []string `json:"allowed_skills"`
	AllowedCapabilities []string `json:"allowed_capabilities"`
	AllowedCodingAgents []string `json:"allowed_coding_agents"`
	AllowedModels       []string `json:"allowed_models"`
	BudgetPolicy        string   `json:"budget_policy"`
}

// UnmarshalJSON deliberately taints external representations even if the caller
// supplies a recognized (or spoofed) execution_class. Generic Save is a trusted
// in-process legacy policy writer, not a portable-import/promotion boundary.
func (p *Project) UnmarshalJSON(data []byte) error {
	type plain Project
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*p = Project(decoded)
	p.imported = true
	return nil
}

func (p Project) ValidExecutionClass() bool {
	return p.ExecutionClass == ExecutionLegacyPolicy || p.ExecutionClass == ExecutionMetadataOnly
}

// Only an actually loaded legacy policy (or the intentional no-project zero
// value) is executable. Empty allowlists remain unrestricted ONLY in that case.
func (p Project) ExecutionAllowed() bool {
	return !p.imported && (p.ExecutionClass == ExecutionLegacyPolicy || (p.ID == "" && p.ExecutionClass == ""))
}
func (p Project) AllowsCapability(id string) bool {
	return p.ExecutionAllowed() && allows(p.AllowedCapabilities, id)
}
func (p Project) AllowsSkill(id string) bool {
	return p.ExecutionAllowed() && allows(p.AllowedSkills, id)
}
func (p Project) AllowsCodingAgent(id string) bool {
	return p.ExecutionAllowed() && allows(p.AllowedCodingAgents, id)
}
func (p Project) AllowsModel(id string) bool {
	return p.ExecutionAllowed() && allows(p.AllowedModels, id)
}
func allows(values []string, id string) bool {
	if len(values) == 0 {
		return true
	}
	for _, v := range values {
		if v == id {
			return true
		}
	}
	return false
}
