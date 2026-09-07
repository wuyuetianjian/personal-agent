package runtime

import (
	"agent/internal/capability"
	"agent/internal/config"
)

func buildCapabilities(cfg config.Config) *capability.Registry {
	items := []capability.Capability{
		{ID: "memory.search", Kind: capability.KindMemory, Description: "Search local episodic and semantic memory", TrustLevel: "local_private", SideEffectLevel: "read_only", PrivacyClasses: []string{"public", "private", "confidential"}, CostClass: "low", LatencyClass: "low", Enabled: true, Health: capability.HealthHealthy, Tags: []string{"local", "memory"}},
		{ID: "rag.search", Kind: capability.KindRetrieval, Description: "Search local indexed documents", TrustLevel: "local_private", SideEffectLevel: "read_only", PrivacyClasses: []string{"public", "private", "confidential"}, CostClass: "low", LatencyClass: "low", Enabled: true, Health: capability.HealthHealthy, Tags: []string{"local", "rag"}},
		{ID: "reasoning.local", Kind: capability.KindReasoning, Description: "Generate private local claims and decisions from persisted evidence", TrustLevel: "local_private", SideEffectLevel: "read_only", PrivacyClasses: []string{"public", "private", "confidential"}, CostClass: "medium", LatencyClass: "medium", Enabled: true, Health: capability.HealthHealthy, Tags: []string{"local", "reasoning", "model_optional"}},
		{ID: "synthesis.local", Kind: capability.KindReasoning, Description: "Synthesize a local-first answer from persisted evidence", TrustLevel: "local_private", SideEffectLevel: "read_only", PrivacyClasses: []string{"public", "private", "confidential"}, CostClass: "low", LatencyClass: "low", Enabled: true, Health: capability.HealthHealthy, Tags: []string{"local", "synthesis"}},
		{ID: "browser.navigate", Kind: capability.KindBrowser, Description: "Navigate a governed browser session", TrustLevel: "local_private", SideEffectLevel: "read_only", PrivacyClasses: []string{"public", "private", "confidential"}, CostClass: "medium", LatencyClass: "medium", Enabled: cfg.Browser.Enabled, Health: capability.HealthHealthy, Tags: []string{"browser"}},
		{ID: "browser.read", Kind: capability.KindBrowser, Description: "Read browser page observations", TrustLevel: "local_private", SideEffectLevel: "read_only", PrivacyClasses: []string{"public", "private", "confidential"}, CostClass: "medium", LatencyClass: "medium", Enabled: cfg.Browser.Enabled, Health: capability.HealthHealthy, Tags: []string{"browser"}},
		{ID: "browser.write", Kind: capability.KindBrowser, Description: "Propose a browser write action", TrustLevel: "local_private", SideEffectLevel: "write", PrivacyClasses: []string{"public", "private", "confidential"}, CostClass: "medium", LatencyClass: "medium", Enabled: cfg.Browser.Enabled, Health: capability.HealthHealthy, Tags: []string{"browser", "approval_required"}},
		{ID: "verification.verify", Kind: capability.KindVerification, Description: "Verify claims against evidence", TrustLevel: "local_private", SideEffectLevel: "read_only", PrivacyClasses: []string{"public", "private", "confidential"}, CostClass: "low", LatencyClass: "low", Enabled: true, Health: capability.HealthHealthy, Tags: []string{"local", "verification"}},
	}
	for id, backend := range cfg.CodingAgents.Backends {
		privacy := []string{"public", "private"}
		if backend.InferenceTrust != "public_remote" {
			privacy = append(privacy, "confidential")
		}
		items = append(items, capability.Capability{ID: "coding." + id, Kind: capability.KindCodingAgent, Description: "Governed external coding agent backend", TrustLevel: backend.InferenceTrust, SideEffectLevel: "write", PrivacyClasses: privacy, CostClass: "high", LatencyClass: "high", Enabled: backend.IsEnabled(), Health: capability.HealthUnknown, Tags: append([]string{"coding", backend.Adapter}, backend.Capabilities...)})
	}
	registry, _ := capability.NewRegistry(items...)
	return registry
}
