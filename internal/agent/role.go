package agent

type Role string

const (
	RoleLeader       Role = "leader"
	RoleResearch     Role = "research"
	RoleRetrieval    Role = "retrieval"
	RoleMemory       Role = "memory"
	RoleReasoning    Role = "reasoning"
	RoleBrowser      Role = "browser"
	RoleTool         Role = "tool"
	RoleVerification Role = "verification"
	RoleSynthesis    Role = "synthesis"
)

type ModelSettings struct {
	ProviderID         string
	ModelID            string
	Temperature        float64
	MaxOutputTokens    int
	TrustLevelRequired string
}
