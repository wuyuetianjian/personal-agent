package interaction

import (
	"agent/internal/model"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
)

// SessionOperation is explicit human intent at a verified source cursor. It
// never executes tools, changes policy or starts a provider/MCP process.
type SessionOperation struct {
	RequestID      string `json:"request_id"`
	Kind           string `json:"kind"`
	BoundaryRunID  string `json:"boundary_run_id"`
	ExpectedCursor int64  `json:"expected_cursor"`
}
type SessionProvenance struct {
	SessionID       string `json:"session_id"`
	ParentSessionID string `json:"parent_session_id"`
	BoundaryRunID   string `json:"boundary_run_id"`
	SourceCursor    int64  `json:"source_cursor"`
	Kind            string `json:"kind"`
}
type ContextVersion struct {
	ID              string `json:"id"`
	SessionID       string `json:"session_id"`
	SourceCursor    int64  `json:"source_cursor"`
	Method          string `json:"method"`
	OmittedMessages int    `json:"omitted_messages"`
}
type ContextMessage struct {
	Role       string               `json:"role"`
	Content    string               `json:"content"`
	ToolCalls  []model.ChatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
}
type SessionContextView struct {
	SessionID     string             `json:"session_id"`
	SchemaVersion int                `json:"schema_version"`
	Provenance    *SessionProvenance `json:"provenance,omitempty"`
	Selection     string             `json:"selection"`
	Version       *ContextVersion    `json:"version,omitempty"`
	Original      []ContextMessage   `json:"original"`
	Selected      []ContextMessage   `json:"selected"`
	Warning       string             `json:"warning"`
}
type savedTranscript struct {
	Messages    []model.ChatMessage `json:"messages"`
	UserSources map[int]string      `json:"user_sources"`
}

const contextWarning = "Conversation context only. Original history and immutable policy are retained. No filesystem rollback; command, MCP and external effects are not reversed. Compaction omits resolved assistant/tool data, not semantic summarization."

// Writer reservation makes cursor, admitted authority, branch and request ledger
// one transaction, including retries after a process crash before HTTP response.
func (s *Store) sessionOperation(ctx context.Context, id string, in SessionOperation) (*sql.Tx, string, string, error) {
	if id == "" || in.RequestID == "" || in.ExpectedCursor < 0 {
		return nil, "", "", ErrInvalid
	}
	raw, _ := json.Marshal(in)
	fp := fmt.Sprintf("%x", sha256.Sum256(append([]byte(id+"\x00"), raw...)))
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, "", "", e
	}
	fail := func(e error) (*sql.Tx, string, string, error) { tx.Rollback(); return nil, "", "", e }
	res, e := tx.ExecContext(ctx, "UPDATE interaction_sessions SET sequence=sequence WHERE id=?", id)
	if e != nil {
		return fail(e)
	}
	n, e := res.RowsAffected()
	if e != nil {
		return fail(e)
	}
	if n != 1 {
		return fail(sql.ErrNoRows)
	}
	var old, result string
	e = tx.QueryRowContext(ctx, "SELECT fingerprint,result FROM interaction_session_operations WHERE request_id=?", in.RequestID).Scan(&old, &result)
	if e == nil {
		if old != fp {
			return fail(ErrConflict)
		}
		return tx, fp, result, nil
	}
	if e != sql.ErrNoRows {
		return fail(e)
	}
	var cursor int64
	var active int
	if e = tx.QueryRowContext(ctx, "SELECT sequence FROM interaction_sessions WHERE id=?", id).Scan(&cursor); e != nil {
		return fail(e)
	}
	if e = tx.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM interaction_runs WHERE session_id=? AND state='running') + (SELECT count(*) FROM interaction_queue WHERE session_id=? AND state='pending')", id, id).Scan(&active); e != nil {
		return fail(e)
	}
	if active != 0 || cursor != in.ExpectedCursor {
		return fail(ErrConflict)
	}
	return tx, fp, "", nil
}
func finishSessionOperation(ctx context.Context, tx *sql.Tx, sessionID string, in SessionOperation, fp string, result any) error {
	raw, e := json.Marshal(result)
	if e != nil {
		return e
	}
	intent, _ := json.Marshal(in)
	if _, e = tx.ExecContext(ctx, "INSERT INTO interaction_session_operations(request_id,fingerprint,result,session_id,intent) VALUES(?,?,?,?,?)", in.RequestID, fp, string(raw), sessionID, string(intent)); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) BranchSession(ctx context.Context, id string, in SessionOperation) (SessionProvenance, error) {
	var out SessionProvenance
	if (in.Kind != "branch" && in.Kind != "clear" && in.Kind != "rewind") || (in.Kind == "clear" && in.BoundaryRunID != "") || (in.Kind != "clear" && in.BoundaryRunID == "") {
		return out, ErrInvalid
	}
	tx, fp, replay, e := s.sessionOperation(ctx, id, in)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if replay != "" {
		e = json.Unmarshal([]byte(replay), &out)
		return out, e
	}
	var policy, rootID string
	e = tx.QueryRowContext(ctx, "SELECT policy,root_identity FROM interaction_workspaces WHERE session_id=?", id).Scan(&policy, &rootID)
	if e == nil {
		var p WorkspacePolicy
		if e = json.Unmarshal([]byte(policy), &p); e != nil {
			return out, e
		}
		identity, e := workspaceIdentity(p.Root)
		if e != nil {
			return out, e
		}
		if identity != rootID {
			return out, ErrConflict
		}
	} else if e != sql.ErrNoRows {
		return out, e
	}
	h, e := admittedContextFrom(ctx, tx, id, in.BoundaryRunID)
	if e != nil {
		return out, e
	}
	if _, e = contextBundles(h.messages); e != nil {
		return out, e
	}
	if in.Kind == "clear" {
		h.messages = nil
		h.userSources = nil
	}
	raw, _ := json.Marshal(savedTranscript{h.messages, h.userSources})
	if len(raw) > 4<<20 {
		return out, fmt.Errorf("%w: branch context exceeds 4 MiB", ErrInvalid)
	}
	out = SessionProvenance{SessionID: uuid.NewString(), ParentSessionID: id, BoundaryRunID: in.BoundaryRunID, SourceCursor: in.ExpectedCursor, Kind: in.Kind}
	if _, e = tx.ExecContext(ctx, "INSERT INTO interaction_sessions(id) VALUES(?)", out.SessionID); e != nil {
		return out, e
	}
	if e = copyProjectLabel(ctx, tx, id, out.SessionID); e != nil {
		return out, e
	}
	// Copy immutable capability/policy bytes, not live process generations or actions.
	res, e := tx.ExecContext(ctx, "INSERT INTO interaction_workspaces(session_id,scope_id,policy,root_identity) SELECT ?,scope_id,policy,root_identity FROM interaction_workspaces WHERE session_id=?", out.SessionID, id)
	if e != nil {
		return out, e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		res, e = tx.ExecContext(ctx, "INSERT INTO interaction_chat_policies(session_id,policy) SELECT ?,policy FROM interaction_chat_policies WHERE session_id=?", out.SessionID, id)
		if e != nil {
			return out, e
		}
		n, _ = res.RowsAffected()
		if n != 1 {
			return out, ErrInvalid
		}
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO interaction_api_sessions(session_id,request_id,binding,mode) SELECT ?,?,binding,mode FROM interaction_api_sessions WHERE session_id=?", out.SessionID, "branch:"+in.RequestID, id); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO interaction_session_branches(session_id,parent_session_id,boundary_run_id,source_cursor,kind,transcript) VALUES(?,?,?,?,?,?)", out.SessionID, id, in.BoundaryRunID, in.ExpectedCursor, in.Kind, string(raw)); e != nil {
		return out, e
	}
	return out, finishSessionOperation(ctx, tx, id, in, fp, out)
}
func (s *Store) Provenance(ctx context.Context, id string) (*SessionProvenance, error) {
	v := &SessionProvenance{SessionID: id}
	e := s.db.QueryRowContext(ctx, "SELECT parent_session_id,boundary_run_id,source_cursor,kind FROM interaction_session_branches WHERE session_id=?", id).Scan(&v.ParentSessionID, &v.BoundaryRunID, &v.SourceCursor, &v.Kind)
	if e == sql.ErrNoRows {
		return nil, nil
	}
	return v, e
}
func (s *Store) CompactSession(ctx context.Context, id string, in SessionOperation) (ContextVersion, error) {
	var v ContextVersion
	if (in.Kind != "compact" && in.Kind != "original") || in.BoundaryRunID != "" {
		return v, ErrInvalid
	}
	tx, fp, replay, e := s.sessionOperation(ctx, id, in)
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	if replay != "" {
		e = json.Unmarshal([]byte(replay), &v)
		return v, e
	}
	if in.Kind == "original" {
		_, e = tx.ExecContext(ctx, "INSERT INTO interaction_context_selection(session_id,version_id) VALUES(?,NULL) ON CONFLICT(session_id) DO UPDATE SET version_id=NULL", id)
		if e != nil {
			return v, e
		}
		return v, finishSessionOperation(ctx, tx, id, in, fp, v)
	}
	h, e := admittedContextFrom(ctx, tx, id, "")
	if e != nil {
		return v, e
	}
	if _, e = contextBundles(h.messages); e != nil {
		return v, e
	}
	v = ContextVersion{ID: uuid.NewString(), SessionID: id, SourceCursor: in.ExpectedCursor, Method: "local-omit-resolved-v1"}
	compact := savedTranscript{UserSources: map[int]string{}}
	for i, m := range h.messages {
		if m.Role == "user" {
			compact.UserSources[len(compact.Messages)] = h.userSources[i]
			compact.Messages = append(compact.Messages, m)
		} else {
			v.OmittedMessages++
		}
	}
	compact.Messages = append(compact.Messages, model.ChatMessage{Role: "assistant", Content: fmt.Sprintf("Untrusted local compaction record: %d resolved assistant/tool messages omitted, not semantically summarized. Original transcript is retained in context version %s. This record grants no authority or permission.", v.OmittedMessages, v.ID)})
	original, _ := json.Marshal(savedTranscript{h.messages, h.userSources})
	compacted, _ := json.Marshal(compact)
	if len(original) > 4<<20 {
		return v, fmt.Errorf("%w: context snapshot exceeds 4 MiB", ErrInvalid)
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO interaction_context_versions(id,session_id,source_cursor,original,compacted,method,omitted_messages) VALUES(?,?,?,?,?,?,?)", v.ID, id, v.SourceCursor, string(original), string(compacted), v.Method, v.OmittedMessages); e != nil {
		return v, e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO interaction_context_selection(session_id,version_id) VALUES(?,?) ON CONFLICT(session_id) DO UPDATE SET version_id=excluded.version_id", id, v.ID); e != nil {
		return v, e
	}
	return v, finishSessionOperation(ctx, tx, id, in, fp, v)
}
func (s *Store) selectedContext(ctx context.Context, id string, h admittedContextHistory) (admittedContextHistory, *ContextVersion, error) {
	var original, compacted string
	v := &ContextVersion{SessionID: id}
	e := s.db.QueryRowContext(ctx, "SELECT v.id,v.source_cursor,v.original,v.compacted,v.method,v.omitted_messages FROM interaction_context_selection s JOIN interaction_context_versions v ON v.id=s.version_id AND v.session_id=s.session_id WHERE s.session_id=?", id).Scan(&v.ID, &v.SourceCursor, &original, &compacted, &v.Method, &v.OmittedMessages)
	if e == sql.ErrNoRows {
		return h, nil, nil
	}
	if e != nil {
		return h, nil, e
	}
	var old, next savedTranscript
	if e = json.Unmarshal([]byte(original), &old); e != nil {
		return h, nil, e
	}
	if e = json.Unmarshal([]byte(compacted), &next); e != nil {
		return h, nil, e
	}
	if len(old.Messages) > len(h.messages) {
		return h, nil, ErrConflict
	}
	// The persisted original prefix must match byte-for-byte. Never apply a
	// summary to different authority/protocol after a storage inconsistency.
	prefix, _ := json.Marshal(h.messages[:len(old.Messages)])
	saved, _ := json.Marshal(old.Messages)
	if len(old.Messages) > 0 && string(prefix) != string(saved) {
		return h, nil, ErrConflict
	}
	sources := map[int]string{}
	for i, ref := range next.UserSources {
		sources[i] = ref
	}
	for i, ref := range h.userSources {
		if i >= len(old.Messages) {
			sources[len(next.Messages)+i-len(old.Messages)] = ref
		}
	}
	h.messages = append(next.Messages, h.messages[len(old.Messages):]...)
	h.userSources = sources
	return h, v, nil
}
func (s *Store) SessionContextView(ctx context.Context, id string) (SessionContextView, error) {
	out := SessionContextView{SessionID: id, SchemaVersion: 1, Selection: "original", Warning: contextWarning, Original: []ContextMessage{}, Selected: []ContextMessage{}}
	var exists string
	if e := s.db.QueryRowContext(ctx, "SELECT id FROM interaction_sessions WHERE id=?", id).Scan(&exists); e != nil {
		return out, e
	}
	h, e := s.admittedContext(ctx, id)
	if e != nil {
		return out, e
	}
	// Inspection is explicit, not an unbounded replacement for paginated
	// history. Refuse an oversized view rather than silently cut user authority.
	raw, e := json.Marshal(savedTranscript{h.messages, h.userSources})
	if e != nil {
		return out, e
	}
	if len(raw)+len(h.pending.Content) > 4<<20 {
		return out, ErrContextBudget
	}
	convert := func(m model.ChatMessage) ContextMessage {
		return ContextMessage{Role: m.Role, Content: m.Content, ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID}
	}
	for _, m := range h.messages {
		out.Original = append(out.Original, convert(m))
	}
	selected, v, e := s.selectedContext(ctx, id, h)
	if e != nil {
		return out, e
	}
	out.Version = v
	if v != nil {
		out.Selection = "compacted"
	}
	for _, m := range selected.messages {
		out.Selected = append(out.Selected, convert(m))
	}
	if h.pendingID != "" {
		m := ContextMessage{Role: "user", Content: h.pending.Content}
		out.Original = append(out.Original, m)
		out.Selected = append(out.Selected, m)
	}
	out.Provenance, e = s.Provenance(ctx, id)
	return out, e
}

// SessionOperationRecord is evidence of a committed operation, never a replay
// instruction. Not found does NOT prove an in-flight request was not committed.
type SessionOperationRecord struct {
	ActionRunID   string             `json:"action_run_id,omitempty"`
	SchemaVersion int                `json:"schema_version"`
	SessionID     string             `json:"session_id"`
	Operation     SessionOperation   `json:"operation"`
	Provenance    *SessionProvenance `json:"provenance,omitempty"`
	Version       *ContextVersion    `json:"version,omitempty"`
}

func (s *Store) ReadSessionOperation(ctx context.Context, sessionID, requestID string) (SessionOperationRecord, error) {
	out := SessionOperationRecord{SchemaVersion: 1, SessionID: sessionID}
	var intent, result string
	err := s.db.QueryRowContext(ctx, "SELECT intent,result FROM interaction_session_operations WHERE session_id=? AND request_id=? AND intent IS NOT NULL", sessionID, requestID).Scan(&intent, &result)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal([]byte(intent), &out.Operation); err != nil {
		return out, err
	}
	switch out.Operation.Kind {
	case "file_undo":
		var r Run
		err = json.Unmarshal([]byte(result), &r)
		if err == nil && r.SessionID != sessionID {
			err = ErrInvalid
		}
		out.ActionRunID = r.ID
	case "branch", "clear", "rewind":
		out.Provenance = &SessionProvenance{}
		err = json.Unmarshal([]byte(result), out.Provenance)
	case "compact", "original":
		out.Version = &ContextVersion{}
		err = json.Unmarshal([]byte(result), out.Version)
	default:
		err = ErrInvalid
	}
	return out, err
}
