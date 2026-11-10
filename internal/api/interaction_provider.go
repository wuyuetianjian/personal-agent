package api

import (
	"agent/internal/model"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
)

type ProviderSelection struct {
	Model                model.ModelMetadata
	Provider             model.StreamProvider
	Endpoint             string
	ToolsSupported       bool
	ToolEvidenceID       string
	ToolCapabilityReason string
}
type ProviderResolver func(context.Context, string) (ProviderSelection, error)

// Binding hashes effective NON-SECRET execution policy. No API keys/env names are persisted.
func (p ProviderSelection) Binding() string {
	raw, _ := json.Marshal(struct {
		ID, Provider, Type, Model, Endpoint string
		Trust, ProviderTrust                model.TrustLevel
		Privacy, Enabled, Tools             bool
		Context                             int
		Capabilities                        map[model.Capability]bool
	}{p.Model.ID, p.Model.ProviderID, p.Model.Provider.Type, p.Model.Model, p.Endpoint, p.Model.TrustLevel, p.Model.Provider.TrustLevel, p.Model.Provider.RequirePrivacyGateway, p.Model.Provider.Enabled, p.ToolsSupported, p.Model.ContextWindow, p.Model.Capabilities})
	// Preserve legacy unknown/fixture bindings; verified generations are additive.
	if p.ToolEvidenceID != "" {
		raw = append(raw, []byte("\nsynthetic-tool-evidence-v1:"+p.ToolEvidenceID)...)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (p ProviderSelection) validate() error {
	u, e := url.Parse(p.Endpoint)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || p.Provider == nil || p.Model.ID == "" || !p.Model.Provider.Enabled {
		return fmt.Errorf("provider configuration unavailable")
	}
	return nil
}
func (p ProviderSelection) safeModel() model.ModelMetadata {
	m := p.Model
	m.Provider.BaseURL = p.Endpoint
	m.Provider.BaseURLEnv = ""
	m.Provider.APIKeyEnv = ""
	return m
}

// Chat-only never gains tools or depends on otherwise irrelevant tool evidence.
func (p ProviderSelection) forMode(mode string) ProviderSelection {
	if mode == "chat_only" {
		p.ToolsSupported = false
		p.ToolEvidenceID = ""
	}
	return p
}

func (p ProviderSelection) toolCapability() string {
	if !p.ToolsSupported {
		return "unverified"
	}
	if p.ToolEvidenceID != "" {
		return "verified_synthetic"
	}
	return "host_supplied"
}
