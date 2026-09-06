package runtime

import (
	"context"

	"agent/internal/audit"
	"agent/internal/capability"
	"agent/internal/config"
	"agent/internal/memory"
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
	rt.ownsStorage = true
	return rt, nil
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
