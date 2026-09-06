package mcp

import "agent/internal/capability"

type Server struct {
	ID             string
	Description    string
	Enabled        bool
	TrustLevel     string
	PrivacyClasses []string
	MaxSideEffect  string
}
type Tool struct {
	ServerID        string
	Name            string
	Description     string
	InputSchema     []byte
	OutputSchema    []byte
	SideEffectLevel string
	ReadOnly        bool
}
type Registry struct {
	Servers map[string]Server
	Tools   map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{Servers: map[string]Server{}, Tools: map[string]Tool{}}
}
func (r *Registry) AddServer(s Server) { r.Servers[s.ID] = s }
func (r *Registry) AddTool(t Tool)     { r.Tools[t.ServerID+"."+t.Name] = t }
func (r *Registry) Capabilities() []capability.Capability {
	out := []capability.Capability{}
	for _, t := range r.Tools {
		s, ok := r.Servers[t.ServerID]
		if !ok || !s.Enabled {
			continue
		}
		id := "mcp." + t.ServerID + "." + t.Name
		out = append(out, capability.Capability{ID: id, Kind: capability.KindMCPTool, Description: t.Description, TrustLevel: s.TrustLevel, SideEffectLevel: t.SideEffectLevel, PrivacyClasses: s.PrivacyClasses, InputSchema: t.InputSchema, OutputSchema: t.OutputSchema, Enabled: true, Health: capability.HealthHealthy, Tags: []string{"mcp", t.ServerID}})
	}
	return out
}
