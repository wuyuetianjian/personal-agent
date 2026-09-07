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
	"agent/internal/project"
	"agent/internal/rag"
	"agent/internal/reliability"
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
	Workflows    workflow.Store
	Projects     project.Store
	Workflow     *WorkflowEngine
	Planner      Planner
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
		Workflows:    workflow.Store{DB: db.SQL},
		Projects:     project.Store{DB: db.SQL},
		Governor:     reliability.NewGovernor(cfg.Reliability.Resources),
		Tracer:       observability.NewTracer(),
		Audit:        audit.Store{DB: db.SQL},
		LeaderModel:  cfg.Agent.Leader.ModelID,
		PrivacyClass: "local_private",
	}
	rt.Workflow = NewWorkflowEngine(rt)
	return rt
}

func (r *Runtime) Close() error {
	if r == nil || !r.ownsStorage || r.Storage == nil {
		return nil
	}
	return r.Storage.Close()
}
