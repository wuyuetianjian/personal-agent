package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"agent/internal/rag"
)

type SemanticFact struct {
	ID           string
	Scope        string
	Subject      string
	Predicate    string
	Object       string
	Source       string
	Confidence   float64
	PrivacyClass string
	EvidenceIDs  []string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type SemanticIndexer interface {
	Index(ctx context.Context, document rag.Document) ([]rag.Chunk, error)
}

type SemanticStore struct {
	db      *sql.DB
	indexer SemanticIndexer
}

func NewSemanticStore(db *sql.DB, indexer SemanticIndexer) SemanticStore {
	return SemanticStore{db: db, indexer: indexer}
}

func (s SemanticStore) Upsert(ctx context.Context, fact SemanticFact) error {
	now := time.Now().UTC()
	if fact.CreatedAt.IsZero() {
		fact.CreatedAt = now
	}
	fact.UpdatedAt = now
	if fact.Confidence == 0 {
		fact.Confidence = 1
	}
	if fact.PrivacyClass == "" {
		fact.PrivacyClass = "local_private"
	}
	evidenceIDs, err := json.Marshal(fact.EvidenceIDs)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO memory_semantic (
  id, scope, subject, predicate, object, source, confidence, privacy_class,
  evidence_ids_json, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  scope = excluded.scope,
  subject = excluded.subject,
  predicate = excluded.predicate,
  object = excluded.object,
  source = excluded.source,
  confidence = excluded.confidence,
  privacy_class = excluded.privacy_class,
  evidence_ids_json = excluded.evidence_ids_json,
  updated_at = excluded.updated_at
`, fact.ID, fact.Scope, fact.Subject, fact.Predicate, fact.Object, fact.Source, fact.Confidence,
		fact.PrivacyClass, string(evidenceIDs), fact.CreatedAt, fact.UpdatedAt)
	if err != nil {
		return err
	}
	if s.indexer == nil {
		return nil
	}
	_, err = s.indexer.Index(ctx, semanticFactDocument(fact))
	return err
}

func (s SemanticStore) FindByScope(ctx context.Context, scope string, limit int) ([]SemanticFact, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, scope, subject, predicate, object, source, confidence, privacy_class,
  evidence_ids_json, created_at, updated_at
FROM memory_semantic
WHERE scope = ?
ORDER BY updated_at DESC, id ASC
LIMIT ?
`, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var facts []SemanticFact
	for rows.Next() {
		fact, err := scanSemanticFact(rows)
		if err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

func semanticFactDocument(fact SemanticFact) rag.Document {
	text := strings.TrimSpace(fact.Subject + " " + fact.Predicate + " " + fact.Object)
	return rag.Document{
		ID:           "semantic_" + fact.ID,
		SourceURI:    "memory://semantic/" + fact.ID,
		Title:        fact.Subject,
		Text:         text,
		Metadata:     map[string]string{"scope": fact.Scope, "source": fact.Source, "evidence_ids": strings.Join(fact.EvidenceIDs, ",")},
		PrivacyClass: fact.PrivacyClass,
		CreatedAt:    fact.CreatedAt,
	}
}

type semanticScanner interface {
	Scan(dest ...any) error
}

func scanSemanticFact(scanner semanticScanner) (SemanticFact, error) {
	var fact SemanticFact
	var evidenceJSON string
	err := scanner.Scan(&fact.ID, &fact.Scope, &fact.Subject, &fact.Predicate, &fact.Object, &fact.Source,
		&fact.Confidence, &fact.PrivacyClass, &evidenceJSON, &fact.CreatedAt, &fact.UpdatedAt)
	if err != nil {
		return SemanticFact{}, err
	}
	if err := json.Unmarshal([]byte(evidenceJSON), &fact.EvidenceIDs); err != nil {
		return SemanticFact{}, err
	}
	return fact, nil
}
