package model

import "fmt"

type Role string

const (
	RoleLeader       Role = "leader"
	RoleResearch     Role = "research"
	RoleBrowser      Role = "browser"
	RoleVerification Role = "verification"
)

type RoleSettings struct {
	ModelID            string
	Temperature        float64
	MaxOutputTokens    int
	TrustLevelRequired TrustLevel
}

type SelectionRequest struct {
	Role         Role
	Capabilities []Capability
}

type Selection struct {
	Model           ModelMetadata
	Temperature     float64
	MaxOutputTokens int
}

type Policy struct {
	registry *Registry
	roles    map[Role]RoleSettings
}

func NewPolicy(registry *Registry, roles map[Role]RoleSettings) *Policy {
	copied := make(map[Role]RoleSettings, len(roles))
	for role, settings := range roles {
		copied[role] = settings
	}
	return &Policy{registry: registry, roles: copied}
}

func (p *Policy) Select(request SelectionRequest) (Selection, error) {
	settings, ok := p.roles[request.Role]
	if !ok {
		return Selection{}, fmt.Errorf("%w: role %s has no model settings", ErrNoMatchingModel, request.Role)
	}
	model, err := p.registry.Model(settings.ModelID)
	if err != nil {
		return Selection{}, err
	}
	if !TrustAllows(settings.TrustLevelRequired, model.TrustLevel) {
		return Selection{}, fmt.Errorf("%w: model %s trust %s exceeds required %s", ErrNoMatchingModel, model.ID, model.TrustLevel, settings.TrustLevelRequired)
	}
	for _, capability := range request.Capabilities {
		if !model.Capabilities[capability] {
			return Selection{}, fmt.Errorf("%w: model %s lacks capability %s", ErrNoMatchingModel, model.ID, capability)
		}
	}
	return Selection{
		Model:           model,
		Temperature:     settings.Temperature,
		MaxOutputTokens: settings.MaxOutputTokens,
	}, nil
}
