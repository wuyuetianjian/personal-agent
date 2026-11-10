package api

import (
	"agent/internal/interaction"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"
)

type KnowledgePage struct {
	SchemaVersion int                         `json:"schema_version"`
	Namespace     string                      `json:"namespace"`
	SessionID     string                      `json:"session_id"`
	ScopeID       string                      `json:"scope_id"`
	Items         []interaction.KnowledgeItem `json:"items"`
	NextCursor    string                      `json:"next_cursor"`
}
type KnowledgeSearch struct {
	SchemaVersion int                          `json:"schema_version"`
	Namespace     string                       `json:"namespace"`
	SessionID     string                       `json:"session_id"`
	ScopeID       string                       `json:"scope_id"`
	Query         string                       `json:"query"`
	Engine        string                       `json:"engine"`
	Matches       []interaction.KnowledgeMatch `json:"matches"`
}

var knowledgeID = regexp.MustCompile(`^knowledge-[a-f0-9]{64}$`)

func knowledgeRoute(p []string) bool { return len(p) >= 3 && p[0] == "sessions" && p[2] == "knowledge" }
func knowledgeQuery(q url.Values, allowed ...string) bool {
	for k, v := range q {
		if len(v) != 1 {
			return false
		}
		ok := false
		for _, a := range allowed {
			if k == a {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return true
}
func (s *InteractionService) knowledge(w http.ResponseWriter, r *http.Request, p []string) bool {
	if !knowledgeRoute(p) {
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if !utf8.ValidString(p[1]) || p[1] == "" || len(p[1]) > 128 {
		wireError(w, 400, "knowledge_invalid_request")
		return true
	}
	if e := s.operatorStoredSession(ctx, p[1]); e != nil {
		wireError(w, 404, "session_not_found")
		return true
	}
	var out any
	var e error
	q := r.URL.Query()
	if r.Method == "GET" && len(p) == 3 || r.Method == "GET" && len(p) == 4 && p[3] == "search" {
		search := len(p) == 4
		allowed := []string{"limit", "cursor"}
		max, limit := 50, 20
		if search {
			allowed = []string{"limit", "q"}
			max, limit = 8, 8
		}
		if !knowledgeQuery(q, allowed...) {
			wireError(w, 400, "invalid_query")
			return true
		}
		if q.Has("limit") {
			v := q.Get("limit")
			limit, e = strconv.Atoi(v)
			if e != nil || strconv.Itoa(limit) != v || limit < 1 || limit > max {
				wireError(w, 400, "invalid_query")
				return true
			}
		}
		var scope string
		// Scope is trusted stored metadata, never caller selected; Store revalidates it.
		_ = s.runtime.DB.SQL.QueryRowContext(ctx, `SELECT scope_id FROM interaction_workspaces WHERE session_id=?`, p[1]).Scan(&scope)
		if search {
			var items []interaction.KnowledgeMatch
			items, e = s.store.SearchKnowledge(ctx, p[1], q.Get("q"), limit)
			out = KnowledgeSearch{1, "runtime-rebuild-v1", p[1], scope, q.Get("q"), "local_bm25", items}
		} else {
			after := ""
			if q.Has("cursor") {
				raw, err := base64.RawURLEncoding.DecodeString(q.Get("cursor"))
				var v []string
				if err != nil || len(raw) > 512 || json.Unmarshal(raw, &v) != nil || len(v) != 2 || v[0] != p[1] || !knowledgeID.MatchString(v[1]) {
					wireError(w, 400, "invalid_cursor")
					return true
				}
				canonical, _ := json.Marshal(v)
				if base64.RawURLEncoding.EncodeToString(canonical) != q.Get("cursor") {
					wireError(w, 400, "invalid_cursor")
					return true
				}
				after = v[1]
			}
			var items []interaction.KnowledgeItem
			items, e = s.store.ListKnowledge(ctx, p[1], after, limit)
			next := ""
			if len(items) == limit {
				raw, _ := json.Marshal([]string{p[1], items[len(items)-1].ID})
				next = base64.RawURLEncoding.EncodeToString(raw)
			}
			out = KnowledgePage{1, "runtime-rebuild-v1", p[1], scope, items, next}
		}
	} else if r.Method == "GET" && len(p) == 5 && p[3] == "operations" {
		if r.URL.RawQuery != "" {
			wireError(w, 400, "invalid_query")
			return true
		}
		out, e = s.store.KnowledgeOperation(ctx, p[1], p[4])
	} else if r.Method == "POST" && len(p) == 4 && (p[3] == "preview" || p[3] == "decision") {
		if r.URL.RawQuery != "" {
			wireError(w, 400, "invalid_query")
			return true
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 400001))
		if err != nil || len(raw) > 400000 || !utf8.Valid(raw) || !knowledgeUnicodeJSON(raw) {
			wireError(w, 400, "invalid_request")
			return true
		}
		fields := []string{"request_id", "kind", "document_id", "source_kind", "source", "content"}
		if p[3] == "decision" {
			fields = []string{"request_id", "operation_id", "digest", "decision"}
		}
		v, ok := operatorBody(raw, fields)
		if !ok {
			wireError(w, 400, "invalid_request")
			return true
		}
		if p[3] == "preview" {
			out, e = s.store.PrepareKnowledge(ctx, p[1], interaction.KnowledgeIntent{RequestID: v["request_id"], Kind: v["kind"], DocumentID: v["document_id"], SourceKind: v["source_kind"], Source: v["source"], Content: v["content"]})
		} else {
			out, e = s.store.DecideKnowledge(ctx, p[1], interaction.OperatorDecision{RequestID: v["request_id"], OperationID: v["operation_id"], Digest: v["digest"], Decision: v["decision"]})
		}
	} else {
		wireError(w, 404, "not_found")
		return true
	}
	switch {
	case errors.Is(e, interaction.ErrKnowledgePolicy):
		wireError(w, 403, "knowledge_policy_refused")
	case errors.Is(e, interaction.ErrCapabilityUnavailable):
		wireError(w, 403, "knowledge_native_capability_unavailable")
	case errors.Is(e, interaction.ErrKnowledgeSource):
		wireError(w, 403, "knowledge_source_refused")
	case errors.Is(e, interaction.ErrInvalid), errors.Is(e, interaction.ErrBudget):
		wireError(w, 400, "knowledge_invalid_request")
	case errors.Is(e, interaction.ErrConflict):
		wireError(w, 409, "knowledge_request_conflict")
	case errors.Is(e, sql.ErrNoRows):
		wireError(w, 404, "knowledge_not_found")
	case e != nil:
		wireError(w, 500, "knowledge_outcome_unknown")
	default:
		wireJSON(w, 200, out)
	}
	return true
}

// encoding/json replaces lone surrogate escapes with U+FFFD. Refuse that
// normalization: reviewed bytes must be exactly the caller's Unicode text.
func knowledgeUnicodeJSON(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
