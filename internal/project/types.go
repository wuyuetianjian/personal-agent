package project

type Project struct {
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

func (p Project) AllowsCapability(id string) bool  { return allows(p.AllowedCapabilities, id) }
func (p Project) AllowsSkill(id string) bool       { return allows(p.AllowedSkills, id) }
func (p Project) AllowsCodingAgent(id string) bool { return allows(p.AllowedCodingAgents, id) }
func (p Project) AllowsModel(id string) bool       { return allows(p.AllowedModels, id) }
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
