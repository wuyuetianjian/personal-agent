package agent

type Role string

const (
	RoleLeader       Role = "leader"
	RoleResearch     Role = "research"
	RoleBrowser      Role = "browser"
	RoleVerification Role = "verification"
)

type ModelSettings struct {
	ProviderID         string
	ModelID            string
	Temperature        float64
	MaxOutputTokens    int
	TrustLevelRequired string
}
