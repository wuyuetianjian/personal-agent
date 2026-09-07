package runtime

import (
	"agent/internal/capability"
	"agent/internal/config"
	"agent/internal/mcp"
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
	for id, server := range cfg.MCP.Servers {
		if !server.Enabled {
			continue
		}
		reg := mcp.NewRegistry()
		reg.AddServer(mcp.Server{ID: id, Enabled: true, TrustLevel: server.TrustLevel, PrivacyClasses: server.PrivacyClasses, MaxSideEffect: "unknown"})
		tools := append([]string(nil), server.Tools...)
		if len(tools) == 0 {
			tools = []string{"call"}
		}
		for _, toolName := range tools {
			reg.AddTool(mcp.Tool{ServerID: id, Name: toolName, Description: "Governed MCP stdio tool call", SideEffectLevel: "unknown"})
		}
		items = append(items, reg.Capabilities()...)
	}
	for _, tool := range cfg.Tools.Allowlist {
		if !tool.Enabled {
			continue
		}
		sideEffect := tool.SideEffectLevel
		if sideEffect == "" {
			sideEffect = "read_only"
		}
		items = append(items, capability.Capability{ID: tool.ID, Kind: capability.KindTool, Description: "Allowlisted local tool", TrustLevel: "local_private", SideEffectLevel: sideEffect, PrivacyClasses: []string{"public", "private", "confidential"}, CostClass: "low", LatencyClass: "low", Enabled: true, Health: capability.HealthHealthy, Tags: []string{"tool", "allowlisted"}})
	}
	registry, _ := capability.NewRegistry(items...)
	return registry
}
