package interaction

import (
	"agent/internal/rag"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxKnowledgeBytes = 64 << 10
const knowledgePolicyVersion = "local-utf8-fields160-v1"

var ErrKnowledgeSource = errors.New("knowledge source refused")

var ErrKnowledgePolicy = errors.New("local knowledge indexing requires a workspace in default or acceptEdits mode and exact explicit confirmation; plan and chat_only refuse")

// No caller scope, project, URI, provider, or execution permission is accepted.
type KnowledgeIntent struct {
	RequestID  string `json:"request_id"`
	Kind       string `json:"kind"`
	DocumentID string `json:"document_id"`
	SourceKind string `json:"source_kind"`
	Source     string `json:"source"`
	Content    string `json:"content"`
}
type KnowledgeOperation struct {
	SchemaVersion         int    `json:"schema_version"`
	Namespace             string `json:"namespace"`
	OperationID           string `json:"operation_id"`
	SessionID             string `json:"session_id"`
	ScopeID               string `json:"scope_id"`
	PolicyDigest          string `json:"policy_digest"`
	Kind                  string `json:"kind"`
	DocumentID            string `json:"document_id"`
	SourceKind            string `json:"source_kind"`
	Source                string `json:"source"`
	ContentHash           string `json:"content_sha256"`
	ContentBytes          int    `json:"content_bytes"`
	Excerpt               string `json:"excerpt"`
	IndexPolicy           string `json:"index_policy"`
	ExpectedRevision      string `json:"expected_revision"`
	ExpectedIndexRevision string `json:"expected_index_revision"`
	ResultRevision        string `json:"result_revision"`
	IndexState            string `json:"index_state"`
	ExpiresAtMS           int64  `json:"expires_at_ms"`
	Digest                string `json:"digest"`
	State                 string `json:"state"`
	Decision              string `json:"decision"`
	DecisionRequestID     string `json:"decision_request_id"`
	Reason                string `json:"reason"`
}
type knowledgeDocument struct{ ID, Scope, SourceKind, Source, Content, Hash, Revision, IndexRevision, State string }

func knowledgeDocumentAt(ctx context.Context, tx *sql.Tx, scope, id string) (d knowledgeDocument, e error) {
	e = tx.QueryRowContext(ctx, `SELECT id,scope_id,source_kind,source,content,content_hash,revision,index_revision,state FROM interaction_knowledge_documents WHERE id=? AND scope_id=?`, id, scope).Scan(&d.ID, &d.Scope, &d.SourceKind, &d.Source, &d.Content, &d.Hash, &d.Revision, &d.IndexRevision, &d.State)
	return
}
func knowledgeDigest(p KnowledgeOperation) string { p.Digest = ""; return operatorHash(p) }
func knowledgeAuthority(ctx context.Context, tx *sql.Tx, session string) (w WorkspaceBinding, digest string, e error) {
	return knowledgeAuthorityMode(ctx, tx, session, false)
}
func knowledgeAuthorityMode(ctx context.Context, tx *sql.Tx, session string, readOnly bool) (w WorkspaceBinding, digest string, e error) {
	w.SessionID = session
	var raw, mode string
	e = tx.QueryRowContext(ctx, `SELECT w.scope_id,w.policy,w.root_identity,a.mode FROM interaction_workspaces w JOIN interaction_api_sessions a ON a.session_id=w.session_id WHERE w.session_id=?`, session).Scan(&w.ScopeID, &raw, &w.RootIdentity, &mode)
	if errors.Is(e, sql.ErrNoRows) {
		return w, "", ErrKnowledgePolicy
	}
	if e != nil {
		return
	}
	if e = json.Unmarshal([]byte(raw), &w.Policy); e != nil {
		return
	}
	if (w.Policy.Mode != ModeDefault && w.Policy.Mode != ModeAcceptEdits && !(readOnly && w.Policy.Mode == ModePlan)) || mode != string(w.Policy.Mode) || w.ScopeID == "" {
		return w, "", ErrKnowledgePolicy
	}
	var identity string
	identity, e = workspaceIdentity(w.Policy.Root)
	if e != nil {
		return
	}
	if identity != w.RootIdentity {
		return w, "", ErrConflict
	}
	_, digest, e = operatorAuthority(ctx, tx, session)
	return
}
func knowledgeContent(content string) error {
	if len(content) > MaxKnowledgeBytes {
		return ErrBudget
	}
	if !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return ErrInvalid
	}
	return nil
}
func knowledgeExcerpt(content string) string {
	b := []byte(content)
	if len(b) > 512 {
		b = b[:512]
		for !utf8.Valid(b) {
			b = b[:len(b)-1]
		}
	}
	return string(b)
}
func knowledgeReplay(ctx context.Context, tx *sql.Tx, session, request, fp string) (out KnowledgeOperation, found bool, e error) {
	var owner, old, raw string
	e = tx.QueryRowContext(ctx, `SELECT r.session_id,r.fingerprint,CASE WHEN r.result='' THEN o.result ELSE r.result END FROM interaction_knowledge_requests r JOIN interaction_knowledge_operations o ON o.id=r.operation_id WHERE r.request_id=?`, request).Scan(&owner, &old, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return out, false, nil
	}
	if e != nil {
		return
	}
	if owner != session || old != fp {
		return out, true, ErrConflict
	}
	e = json.Unmarshal([]byte(raw), &out)
	return out, true, e
}
func knowledgeAudit(ctx context.Context, tx *sql.Tx, p KnowledgeOperation) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO security_audit_log(id,actor,action,target,status,reason,evidence_ids,created_at) VALUES(?,?,?,?,?,?,?,?)`, "knowledge:"+p.OperationID+":"+p.State, "session:"+p.SessionID, "knowledge."+p.Kind, p.DocumentID, p.State, p.Reason, "[\""+p.OperationID+"\"]", time.Now().UTC())
	return e
}
func (s *Store) PrepareKnowledge(ctx context.Context, session string, in KnowledgeIntent) (out KnowledgeOperation, e error) {
	if !operatorRequestID(in.RequestID) || !utf8.ValidString(in.Source) || len(in.Source) > 512 || strings.ContainsRune(in.Source, 0) || len(in.DocumentID) > 128 {
		return out, ErrInvalid
	}
	if in.Kind == "ingest" {
		if in.DocumentID != "" || (in.SourceKind != "text" && in.SourceKind != "file") || strings.TrimSpace(in.Source) == "" || (in.SourceKind == "file" && in.Content != "") {
			return out, ErrInvalid
		}
	} else if (in.Kind != "reindex" && in.Kind != "deactivate") || in.DocumentID == "" || in.SourceKind != "" || in.Source != "" || in.Content != "" {
		return out, ErrInvalid
	}
	if e = knowledgeContent(in.Content); e != nil {
		return
	}
	tx, e := s.queueWrite(ctx, session)
	if e != nil {
		return
	}
	defer tx.Rollback()
	fp := operatorHash([]any{session, "prepare", in})
	if old, found, err := knowledgeReplay(ctx, tx, session, in.RequestID, fp); err != nil || found {
		return old, err
	}
	w, policy, e := knowledgeAuthority(ctx, tx, session)
	if e != nil {
		return out, e
	}
	content := in.Content
	kind, source, id := in.SourceKind, in.Source, in.DocumentID
	var previous knowledgeDocument
	if in.Kind != "ingest" {
		previous, e = knowledgeDocumentAt(ctx, tx, w.ScopeID, id)
		if e != nil {
			return out, e
		}
		kind, source, content = previous.SourceKind, previous.Source, previous.Content
	}
	if kind == "file" && in.Kind != "deactivate" {
		var b []byte
		b, e = readWorkspaceFile(ctx, w, source)
		if e != nil {
			return out, fmt.Errorf("%w: %w", ErrKnowledgeSource, e)
		}
		content = string(b)
	}
	if e = knowledgeContent(content); e != nil {
		return out, e
	}
	hash := rag.HashText(content)
	if in.Kind == "ingest" {
		identity := source
		if kind == "text" {
			identity = hash
		}
		id = "knowledge-" + operatorHash([]string{w.ScopeID, kind, identity})
		previous, e = knowledgeDocumentAt(ctx, tx, w.ScopeID, id)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
	}
	var n, staged int
	if e = tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(length(CAST(staged AS BLOB))),0) FROM interaction_knowledge_operations`).Scan(&n, &staged); e != nil {
		return out, e
	}
	// Explicit bounded retained staging; no silent retention/deletion policy.
	if n >= 4096 || staged+len(content) > 64<<20 {
		return out, ErrBudget
	}
	if previous.ID == "" {
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM interaction_knowledge_documents WHERE scope_id=?`, w.ScopeID).Scan(&n); e != nil {
			return out, e
		}
		if n >= 128 {
			return out, ErrBudget
		}
	} else {
		kind, source = previous.SourceKind, previous.Source
	}
	out = KnowledgeOperation{SchemaVersion: 1, Namespace: "runtime-rebuild-v1", OperationID: in.RequestID, SessionID: session, ScopeID: w.ScopeID, PolicyDigest: policy, Kind: in.Kind, DocumentID: id, SourceKind: kind, Source: source, ContentHash: hash, ContentBytes: len(content), Excerpt: knowledgeExcerpt(content), IndexPolicy: knowledgePolicyVersion, ExpectedRevision: previous.Revision, ExpectedIndexRevision: previous.IndexRevision, IndexState: "staged", ExpiresAtMS: time.Now().Add(10 * time.Minute).UnixMilli(), State: "pending", Reason: "Prepared only. Confirm authorizes local indexing of this exact content in this workspace. Content and provenance remain in the private runtime; retrieved text is untrusted evidence and may enter future model context. No remote fetch or embedding call. Remove deactivates retrieval, not retained evidence."}
	out.Digest = knowledgeDigest(out)
	raw, _ := json.Marshal(out)
	if _, e = tx.ExecContext(ctx, `INSERT INTO interaction_knowledge_operations VALUES(?,?,?,?,'pending',?)`, out.OperationID, session, string(raw), content, string(raw)); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO interaction_knowledge_requests VALUES(?,?,?,?,'')`, in.RequestID, session, out.OperationID, fp); e != nil {
		return out, e
	}
	if e = knowledgeAudit(ctx, tx, out); e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (s *Store) KnowledgeOperation(ctx context.Context, session, request string) (out KnowledgeOperation, e error) {
	if !operatorRequestID(request) {
		return out, ErrInvalid
	}
	var raw string
	e = s.db.QueryRowContext(ctx, `SELECT CASE WHEN r.result='' THEN o.result ELSE r.result END FROM interaction_knowledge_requests r JOIN interaction_knowledge_operations o ON o.id=r.operation_id WHERE r.request_id=? AND r.session_id=?`, request, session).Scan(&raw)
	if e != nil {
		return
	}
	e = json.Unmarshal([]byte(raw), &out)
	if e == nil && out.State == "pending" && time.Now().UnixMilli() >= out.ExpiresAtMS {
		out.State = "expired"
		out.Reason = "Preview expired; no index change."
	}
	return
}
func (s *Store) DecideKnowledge(ctx context.Context, session string, in OperatorDecision) (out KnowledgeOperation, e error) {
	if !operatorRequestID(in.RequestID) || !operatorRequestID(in.OperationID) || len(in.Digest) != 64 || (in.Decision != "confirm" && in.Decision != "cancel") {
		return out, ErrInvalid
	}
	tx, e := s.queueWrite(ctx, session)
	if e != nil {
		return
	}
	defer tx.Rollback()
	fp := operatorHash([]any{session, "decision", in})
	if old, found, err := knowledgeReplay(ctx, tx, session, in.RequestID, fp); err != nil || found {
		return old, err
	}
	var raw, state, content string
	e = tx.QueryRowContext(ctx, `SELECT preview,state,staged FROM interaction_knowledge_operations WHERE id=? AND session_id=?`, in.OperationID, session).Scan(&raw, &state, &content)
	if errors.Is(e, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if e != nil {
		return
	}
	if e = json.Unmarshal([]byte(raw), &out); e != nil {
		return
	}
	if state != "pending" || out.Digest != in.Digest || knowledgeDigest(out) != in.Digest {
		return out, ErrConflict
	}
	out.Decision = in.Decision
	out.DecisionRequestID = in.RequestID
	reject := func(reason string) { out.State = "rejected"; out.IndexState = "not_applied"; out.Reason = reason }
	w, policy, authorityErr := knowledgeAuthority(ctx, tx, session)
	switch {
	case time.Now().UnixMilli() >= out.ExpiresAtMS:
		out.State = "expired"
		out.IndexState = "not_applied"
		out.Reason = "Preview expired; no index change."
	case in.Decision == "cancel":
		reject("User cancelled preview; no index change.")
	case authorityErr != nil || w.ScopeID != out.ScopeID || policy != out.PolicyDigest:
		reject("Workspace root or original authority changed or unavailable; no index change.")
	case out.IndexPolicy != knowledgePolicyVersion || knowledgeContent(content) != nil || len(content) != out.ContentBytes || rag.HashText(content) != out.ContentHash:
		reject("Staged content or index policy mismatch; no index change.")
	default:
		previous, err := knowledgeDocumentAt(ctx, tx, out.ScopeID, out.DocumentID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		var live string
		err = tx.QueryRowContext(ctx, `SELECT revision FROM document_revisions WHERE document_id=?`, out.DocumentID).Scan(&live)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if previous.Revision != out.ExpectedRevision || previous.IndexRevision != out.ExpectedIndexRevision || live != out.ExpectedIndexRevision {
			reject("Target revision changed; prepare a new exact preview.")
			break
		}
		if out.SourceKind == "file" && out.Kind != "deactivate" {
			b, err := readWorkspaceFile(ctx, w, out.Source)
			if err != nil || rag.HashText(string(b)) != out.ContentHash {
				reject("Workspace file changed or unavailable; prepare a new exact reread preview.")
				break
			}
		}
		if (out.Kind == "deactivate" && previous.State == "deactivated") || (out.Kind != "deactivate" && previous.Hash == out.ContentHash && (previous.State == "indexed" || previous.State == "empty")) {
			// Matching bytes are not proof of current retrieval authority.
			var grants int
			if out.Kind != "deactivate" {
				if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM project_document_scopes WHERE document_id=? AND project_id=? AND content_hash=? AND revision=?`, out.DocumentID, out.ScopeID, out.ContentHash, live).Scan(&grants); e != nil {
					return out, e
				}
				if grants != 1 {
					reject("Current index ownership missing; no implicit authority repair.")
					break
				}
			}
			out.State = "no_op"
			out.IndexState = previous.State
			out.ResultRevision = previous.Revision
			out.Reason = "Already at requested current content/state; no new index effect."
		} else {
			if e = applyKnowledge(ctx, tx, &out, content, previous); e != nil {
				return out, e
			}
			out.State = "committed"
			out.Reason = "Local content, current index ownership, revision, result and audit committed atomically. No external execution."
		}
	}
	result, _ := json.Marshal(out)
	if _, e = tx.ExecContext(ctx, `UPDATE interaction_knowledge_operations SET state=?,result=? WHERE id=? AND state='pending'`, out.State, string(result), out.OperationID); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO interaction_knowledge_requests VALUES(?,?,?,?,?)`, in.RequestID, session, out.OperationID, fp, string(result)); e != nil {
		return out, e
	}
	if e = knowledgeAudit(ctx, tx, out); e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func applyKnowledge(ctx context.Context, tx *sql.Tx, p *KnowledgeOperation, content string, old knowledgeDocument) error {
	revision := old.IndexRevision
	if p.Kind == "deactivate" {
		if _, e := tx.ExecContext(ctx, `DELETE FROM project_document_scopes WHERE document_id=? AND project_id=?`, p.DocumentID, p.ScopeID); e != nil {
			return e
		}
		p.IndexState = "deactivated"
	} else {
		if old.ID == "" {
			var n int
			if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM interaction_knowledge_documents WHERE scope_id=?`, p.ScopeID).Scan(&n); e != nil {
				return e
			}
			if n >= 128 {
				return ErrBudget
			}
		}
		now := time.Now().UTC()
		_, e := tx.ExecContext(ctx, `INSERT INTO documents(id,source_uri,title,content_hash,metadata_json,privacy_class,created_at,updated_at) VALUES(?,?,?,?,'{}','private',?,?) ON CONFLICT(id) DO UPDATE SET content_hash=excluded.content_hash,updated_at=excluded.updated_at`, p.DocumentID, "knowledge:"+p.DocumentID, p.Source, p.ContentHash, now, now)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM document_chunks WHERE document_id=?`, p.DocumentID); e != nil {
			return e
		}
		chunks := (rag.Chunker{MaxTokens: 160}).Chunk(rag.Document{ID: p.DocumentID, Text: content})
		if len(chunks) > 512 {
			return ErrBudget
		}
		for i, c := range chunks {
			if _, e = tx.ExecContext(ctx, `INSERT INTO document_chunks(id,document_id,chunk_index,text,token_count,content_hash,metadata_json,created_at) VALUES(?,?,?,?,?,?,'{}',?)`, fmt.Sprintf("%s:%d:%s", p.DocumentID, i, c.ContentHash), p.DocumentID, i, c.Text, c.TokenCount, c.ContentHash, now); e != nil {
				return e
			}
		}
		if e = tx.QueryRowContext(ctx, `SELECT revision FROM document_revisions WHERE document_id=?`, p.DocumentID).Scan(&revision); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO project_document_scopes(document_id,project_id,content_hash,revision) SELECT id,?,content_hash,? FROM documents WHERE id=? AND content_hash=? AND (SELECT revision FROM document_revisions WHERE document_id=?)=?`, p.ScopeID, revision, p.DocumentID, p.ContentHash, p.DocumentID, revision); e != nil {
			return e
		}
		p.IndexState = "indexed"
		if len(chunks) == 0 {
			p.IndexState = "empty"
		}
	}
	if e := tx.QueryRowContext(ctx, `SELECT lower(hex(randomblob(32)))`).Scan(&p.ResultRevision); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO interaction_knowledge_documents VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET content=excluded.content,content_hash=excluded.content_hash,revision=excluded.revision,index_revision=excluded.index_revision,state=excluded.state`, p.DocumentID, p.ScopeID, p.SourceKind, p.Source, content, p.ContentHash, p.ResultRevision, revision, p.IndexState)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO interaction_knowledge_versions VALUES(?,?,?,?,?,?,?)`, p.OperationID, p.DocumentID, content, p.ContentHash, p.ResultRevision, revision, p.IndexState)
	return e
}
