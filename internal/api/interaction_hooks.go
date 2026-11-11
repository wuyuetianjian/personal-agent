package api

import (
	"agent/internal/interaction"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func hookRoute(p []string) bool { return len(p) >= 3 && p[0] == "sessions" && p[2] == "hooks" }

// Hook mutations use only the bounded policy ledger. No generic acknowledgement,
// provider resolution, workspace access or worker admission is performed here.
func (s *InteractionService) hooks(w http.ResponseWriter, r *http.Request, p []string) bool {
	if !hookRoute(p) {
		return false
	}
	if r.URL.EscapedPath() != r.URL.Path || r.URL.Path != "/api/v1/"+strings.Join(p, "/") {
		wireError(w, 400, "invalid_request")
		return true
	}
	for _, part := range p {
		if !skillWireIdentity(part) {
			wireError(w, 400, "invalid_request")
			return true
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if e := s.operatorStoredSession(ctx, p[1]); e != nil {
		wireError(w, 404, "session_not_found")
		return true
	}
	var out any
	var e error
	if r.Method == "GET" && len(p) == 4 && p[3] == "revisions" {
		q := r.URL.Query()
		if !knowledgeQuery(q, "limit", "cursor") {
			wireError(w, 400, "invalid_query")
			return true
		}
		limit := 20
		if q.Has("limit") {
			limit, e = strconv.Atoi(q.Get("limit"))
			if e != nil || strconv.Itoa(limit) != q.Get("limit") || limit < 1 || limit > 32 {
				wireError(w, 400, "invalid_query")
				return true
			}
		}
		if q.Has("cursor") && q.Get("cursor") == "" {
			wireError(w, 400, "invalid_query")
			return true
		}
		out, e = s.store.ListHookRevisions(ctx, p[1], q.Get("cursor"), limit)
	} else if r.URL.RawQuery != "" {
		wireError(w, 400, "invalid_query")
		return true
	} else if r.Method == "GET" && len(p) == 3 {
		out, e = s.store.HookSelection(ctx, p[1])
	} else if r.Method == "GET" && len(p) == 5 && p[3] == "revisions" {
		out, e = s.store.HookRevision(ctx, p[1], p[4])
	} else if r.Method == "GET" && len(p) == 5 && p[3] == "operations" {
		out, e = s.store.HookOperation(ctx, p[1], p[4])
	} else if r.Method == "POST" && len(p) == 4 && (p[3] == "preview" || p[3] == "decision") {
		cap := 53248
		if p[3] == "decision" {
			cap = 4096
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, int64(cap+1)))
		if err != nil || len(raw) > cap {
			wireError(w, 400, "invalid_request")
			return true
		}
		if p[3] == "preview" {
			in, err := interaction.DecodeHookIntent(raw)
			if err != nil || !skillWireIdentity(in.RequestID) {
				wireError(w, 400, "invalid_request")
				return true
			}
			out, e = s.store.PrepareHooks(ctx, p[1], in)
		} else {
			v, ok := operatorBody(raw, []string{"request_id", "operation_id", "digest", "decision"})
			if !ok || !skillWireIdentity(v["request_id"]) || !skillWireIdentity(v["operation_id"]) {
				wireError(w, 400, "invalid_request")
				return true
			}
			var op interaction.HookOperation
			op, e = s.store.DecideHooks(ctx, p[1], interaction.OperatorDecision{RequestID: v["request_id"], OperationID: v["operation_id"], Digest: v["digest"], Decision: v["decision"]})
			out = op
			if e == nil && op.State == "applied" && (op.Reason == "enabled" || op.Reason == "disabled") {
				s.store.SignalRevokedSessionSkillRuns(ctx, p[1])
			}
		}
	} else {
		wireError(w, 404, "not_found")
		return true
	}
	switch {
	case errors.Is(e, interaction.ErrInvalid):
		wireError(w, 400, "invalid_request")
	case errors.Is(e, interaction.ErrConflict):
		wireError(w, 409, "conflict")
	case errors.Is(e, interaction.ErrBudget):
		wireError(w, 409, "budget_exhausted")
	case errors.Is(e, sql.ErrNoRows):
		wireError(w, 404, "not_found")
	case e != nil:
		wireError(w, 500, "outcome_unknown")
	default:
		wireJSON(w, 200, out)
	}
	return true
}
