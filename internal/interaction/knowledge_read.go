package interaction

import (
	"agent/internal/rag"
	"context"
	"database/sql"
	"strings"
	"unicode/utf8"
)

// Inventory does not return original content or reopen stored sources.
type KnowledgeItem struct {
	ID            string `json:"id"`
	ScopeID       string `json:"scope_id"`
	SourceKind    string `json:"source_kind"`
	Source        string `json:"source"`
	ContentHash   string `json:"content_sha256"`
	ContentBytes  int    `json:"content_bytes"`
	Revision      string `json:"revision"`
	IndexRevision string `json:"index_revision"`
	State         string `json:"state"`
}
type KnowledgeMatch struct {
	DocumentID string  `json:"document_id"`
	ChunkID    string  `json:"chunk_id"`
	Revision   string  `json:"revision"`
	SourceKind string  `json:"source_kind"`
	Source     string  `json:"source"`
	Text       string  `json:"text"`
	Score      float64 `json:"score"`
}

const knowledgeCurrent = ` k.index_revision=(SELECT revision FROM document_revisions WHERE document_id=k.id) AND EXISTS(SELECT 1 FROM project_document_scopes p JOIN documents d ON d.id=p.document_id WHERE p.document_id=k.id AND p.project_id=k.scope_id AND p.content_hash=k.content_hash AND d.content_hash=k.content_hash AND p.revision=k.index_revision) `

func (s *Store) ListKnowledge(ctx context.Context, session, after string, limit int) (out []KnowledgeItem, e error) {
	if limit < 1 || limit > 50 || len(after) > 128 || !utf8.ValidString(after) {
		return nil, ErrInvalid
	}
	tx, e := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	w, _, e := knowledgeAuthorityMode(ctx, tx, session, true)
	if e != nil {
		return nil, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT k.id,k.scope_id,k.source_kind,k.source,k.content_hash,length(CAST(k.content AS BLOB)),k.revision,k.index_revision,CASE WHEN k.state='deactivated' THEN 'deactivated' WHEN `+knowledgeCurrent+` THEN k.state ELSE 'stale' END FROM interaction_knowledge_documents k WHERE k.scope_id=? AND k.id>? ORDER BY k.id LIMIT ?`, w.ScopeID, after, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out = []KnowledgeItem{}
	for rows.Next() {
		var k KnowledgeItem
		if e = rows.Scan(&k.ID, &k.ScopeID, &k.SourceKind, &k.Source, &k.ContentHash, &k.ContentBytes, &k.Revision, &k.IndexRevision, &k.State); e != nil {
			return nil, e
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

type knowledgeCorpus []rag.Result

func (c knowledgeCorpus) ListChunks(ctx context.Context) ([]rag.Result, error) { return c, ctx.Err() }

// SearchKnowledge is local BM25 over verified current scoped chunks, not a
// provider configuration/evidence proxy. Original hostile content has no authority.
func (s *Store) SearchKnowledge(ctx context.Context, session, query string, limit int) (out []KnowledgeMatch, e error) {
	if limit < 1 || limit > 8 || len(query) > 1024 || !utf8.ValidString(query) || strings.TrimSpace(query) == "" || strings.ContainsRune(query, 0) {
		return nil, ErrInvalid
	}
	tx, e := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	w, _, e := knowledgeAuthorityMode(ctx, tx, session, true)
	if e != nil {
		return nil, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT c.id,c.document_id,c.text,k.index_revision,k.source_kind,k.source FROM interaction_knowledge_documents k JOIN document_chunks c ON c.document_id=k.id WHERE k.scope_id=? AND k.state='indexed' AND `+knowledgeCurrent+` ORDER BY k.id,c.chunk_index LIMIT 65537`, w.ScopeID)
	if e != nil {
		return nil, e
	}
	var corpus knowledgeCorpus
	meta := map[string]KnowledgeMatch{}
	total := 0
	for rows.Next() {
		var m KnowledgeMatch
		if e = rows.Scan(&m.ChunkID, &m.DocumentID, &m.Text, &m.Revision, &m.SourceKind, &m.Source); e != nil {
			rows.Close()
			return nil, e
		}
		total += len(m.Text)
		if len(corpus) >= 65536 || total > 128*MaxKnowledgeBytes {
			rows.Close()
			return nil, ErrBudget
		}
		meta[m.ChunkID] = m
		corpus = append(corpus, rag.Result{Chunk: rag.Chunk{ID: m.ChunkID, DocumentID: m.DocumentID, Text: m.Text}})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	results, e := rag.NewBM25(corpus).Search(ctx, query, limit)
	if e != nil {
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	out = []KnowledgeMatch{}
	for _, r := range results {
		m := meta[r.Chunk.ID]
		m.Score = r.Score
		out = append(out, m)
	}
	return out, nil
}
