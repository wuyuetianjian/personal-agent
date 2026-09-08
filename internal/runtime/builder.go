package runtime

import (
	"context"

	"agent/internal/audit"
	"agent/internal/capability"
	"agent/internal/config"
	"agent/internal/memory"
	"agent/internal/model"
	"agent/internal/observability"
	"agent/internal/orchestrator"
	"agent/internal/privacy"
	"agent/internal/project"
	"agent/internal/rag"
	"agent/internal/reliability"
	"agent/internal/skill"
	"agent/internal/storage"
	"agent/internal/verification"
	"agent/internal/workflow"
)

type Runtime struct {
	Config       config.Config
	Storage      *storage.DB
	Events       orchestrator.EvidenceBus
	RAG          Pipeline
	Memory       MemoryService
	Evidence     EvidenceStore
	Verifier     verification.Verifier
	Capabilities *capability.Registry
	Skills       *skill.Registry
	Workflows    workflow.Store
	Projects     project.Store
	Workflow     *WorkflowEngine
	Planner      Planner
	Executors    *CapabilityExecutorRegistry
	Escalator    *PublicEscalator
	ChatProvider model.ChatProvider
	ChatModel    model.ModelMetadata
	Governor     *reliability.Governor
	Tracer       *observability.Tracer
	Audit        audit.Store
	ownsStorage  bool
	LeaderModel  string
	PrivacyClass string
}

func Build(ctx context.Context, cfg config.Config) (*Runtime, error) {
	db, err := storage.Open(ctx, cfg.Storage)
	if err != nil {
		return nil, err
	}
	if err := storage.Migrate(ctx, db.SQL); err != nil {
		db.Close()
		return nil, err
	}
	rt := NewLocal(cfg, db, &orchestrator.InMemoryEvidenceBus{})
	provider, metadata, err := buildConfiguredChatProvider(cfg)
	if err != nil {
		db.Close()
		return nil, err
	}
	rt.ChatProvider = provider
	rt.ChatModel = metadata
	rt.Planner = buildConfiguredPlanner(cfg, provider, metadata, rt.Capabilities)
	escalator, err := buildConfiguredPublicEscalator(cfg)
	if err != nil {
		db.Close()
		return nil, err
	}
	rt.Escalator = escalator
	if err := configureHybridRAG(cfg, db.SQL, rt); err != nil {
		db.Close()
		return nil, err
	}
	rt.ownsStorage = true
	return rt, nil
}

func buildConfiguredPlanner(cfg config.Config, provider model.ChatProvider, metadata model.ModelMetadata, capabilities *capability.Registry) Planner {
	if provider == nil || capabilities == nil || !cfg.Agent.Planner.IsEnabled() {
		return nil
	}
	if !metadata.Capabilities[model.CapabilityChat] || !metadata.Capabilities[model.CapabilityJSONSchema] {
		return nil
	}
	return BoundedModelPlanner{
		Provider:        provider,
		Model:           metadata,
		Capabilities:    capabilities,
		Temperature:     cfg.Agent.Leader.Temperature,
		MaxOutputTokens: cfg.Agent.Leader.MaxOutputTokens,
		MaxNodes:        cfg.Agent.Planner.MaxNodes,
	}
}

func buildConfiguredChatProvider(cfg config.Config) (model.ChatProvider, model.ModelMetadata, error) {
	if cfg.Agent.Leader.ModelID == "" || len(cfg.Models.Registry) == 0 {
		return nil, model.ModelMetadata{}, nil
	}
	registry, err := model.NewRegistry(cfg.Models)
	if err != nil {
		return nil, model.ModelMetadata{}, err
	}
	metadata, err := registry.Model(cfg.Agent.Leader.ModelID)
	if err != nil {
		return nil, model.ModelMetadata{}, err
	}
	if !metadata.Provider.Enabled {
		return nil, metadata, nil
	}
	provider, err := buildOpenAICompatibleClient(metadata)
	return provider, metadata, err
}

func buildConfiguredPublicEscalator(cfg config.Config) (*PublicEscalator, error) {
	if len(cfg.Models.Registry) == 0 {
		return nil, nil
	}
	registry, err := model.NewRegistry(cfg.Models)
	if err != nil {
		return nil, err
	}
	for _, metadata := range registry.Models() {
		if metadata.Provider.TrustLevel != model.TrustPublicRemote || !metadata.Provider.Enabled || !metadata.Capabilities[model.CapabilityChat] {
			continue
		}
		client, err := buildOpenAICompatibleClient(metadata)
		if err != nil {
			return nil, err
		}
		if metadata.Provider.RequirePrivacyGateway || cfg.Privacy.FailClosedForPublicModels {
			secret, ok := config.EnvValue(cfg.Privacy.HMACSecretEnv)
			if !ok {
				return nil, model.ErrPrivacyGatewayRequired
			}
			gateway, err := privacy.NewGateway(secret)
			if err != nil {
				return nil, err
			}
			client.Privacy = gateway
		}
		return &PublicEscalator{Provider: client, Model: metadata}, nil
	}
	return nil, nil
}

func NewLocal(cfg config.Config, db *storage.DB, events orchestrator.EvidenceBus) *Runtime {
	ragStore := rag.NewSQLiteStore(db.SQL, rag.Chunker{MaxTokens: 160})
	pipeline := LocalPipeline{
		BM25:       rag.NewBM25(ragStore),
		Compressor: rag.Compressor{MaxChars: 4000},
		TopK:       8,
	}
	rt := &Runtime{
		Config:       cfg,
		Storage:      db,
		Events:       events,
		RAG:          pipeline,
		Memory:       SQLiteMemoryService{Episodic: memory.NewStore(db.SQL), Semantic: memory.NewSemanticStore(db.SQL, ragStore)},
		Evidence:     SQLiteEvidenceStore{DB: db.SQL},
		Verifier:     verification.Verifier{Policy: verification.DefaultPolicy()},
		Capabilities: buildCapabilities(cfg),
		Skills:       skill.NewRegistry(),
		Workflows:    workflow.Store{DB: db.SQL},
		Projects:     project.Store{DB: db.SQL},
		Executors:    NewCapabilityExecutorRegistry(),
		Governor:     reliability.NewGovernor(cfg.Reliability.Resources),
		Tracer:       observability.NewTracer(),
		Audit:        audit.Store{DB: db.SQL},
		LeaderModel:  cfg.Agent.Leader.ModelID,
		PrivacyClass: "local_private",
	}
	rt.Workflow = NewWorkflowEngine(rt)
	configureCapabilityExecutors(rt)
	return rt
}

func configureCapabilityExecutors(rt *Runtime) {
	if rt == nil || rt.Executors == nil {
		return
	}
	if rt.Config.Browser.Enabled {
		executor := NewBrowserExecutor(rt.Config.Browser)
		executor.Evidence = rt.Evidence
		rt.Executors.Register("browser.navigate", executor)
		rt.Executors.Register("browser.read", executor)
		rt.Executors.Register("browser.write", executor)
	}
	for id, backend := range rt.Config.CodingAgents.Backends {
		if !backend.IsEnabled() {
			continue
		}
		rt.Executors.Register("coding."+id, NewCodingExecutor(rt.Config, id, rt.Evidence))
	}
	for id, server := range rt.Config.MCP.Servers {
		if !server.Enabled {
			continue
		}
		tools := append([]string(nil), server.Tools...)
		if len(tools) == 0 {
			tools = []string{"call"}
		}
		for _, toolName := range tools {
			rt.Executors.Register("mcp."+id+"."+toolName, MCPExecutor{ServerID: id, ToolName: toolName, Config: server, Evidence: rt.Evidence})
		}
	}
	for _, tool := range rt.Config.Tools.Allowlist {
		if tool.Enabled {
			rt.Executors.Register(tool.ID, ToolExecutor{Config: tool, Evidence: rt.Evidence})
		}
	}
}

func (r *Runtime) Close() error {
	if r == nil || !r.ownsStorage || r.Storage == nil {
		return nil
	}
	return r.Storage.Close()
}
