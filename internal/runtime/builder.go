package runtime

import (
	"context"

	"agent/internal/config"
	"agent/internal/memory"
	"agent/internal/orchestrator"
	"agent/internal/rag"
	"agent/internal/storage"
	"agent/internal/verification"
)

type Runtime struct {
	Config       config.Config
	Storage      *storage.DB
	Events       orchestrator.EvidenceBus
	RAG          Pipeline
	Memory       MemoryService
	Evidence     EvidenceStore
	Verifier     verification.Verifier
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
	return &Runtime{
		Config:       cfg,
		Storage:      db,
		Events:       events,
		RAG:          pipeline,
		Memory:       SQLiteMemoryService{Episodic: memory.NewStore(db.SQL), Semantic: memory.NewSemanticStore(db.SQL, ragStore)},
		Evidence:     SQLiteEvidenceStore{DB: db.SQL},
		Verifier:     verification.Verifier{Policy: verification.DefaultPolicy()},
		LeaderModel:  cfg.Agent.Leader.ModelID,
		PrivacyClass: "local_private",
	}
}

func (r *Runtime) Close() error {
	if r == nil || !r.ownsStorage || r.Storage == nil {
		return nil
	}
	return r.Storage.Close()
}
