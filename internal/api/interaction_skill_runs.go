package api

import (
	"agent/internal/interaction"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type SkillRunControlReceipt struct {
	SchemaVersion int    `json:"schema_version"`
	SessionID     string `json:"session_id"`
	RequestID     string `json:"request_id"`
	RunID         string `json:"run_id"`
	Action        string `json:"action"`
	State         string `json:"state"`
	Phase         string `json:"phase"`
}

func (s *InteractionService) skillControlReceipt(ctx context.Context, session, run, action, request string) (out SkillRunControlReceipt, err error) {
	if action != "cancel" && action != "resume" {
		return out, interaction.ErrInvalid
	}
	var reservation string
	var ackFP, raw sql.NullString
	err = s.runtime.DB.SQL.QueryRowContext(ctx, `SELECT c.fingerprint,a.fingerprint,a.result FROM interaction_skill_controls c JOIN interaction_skill_runs s ON s.run_id=c.run_id LEFT JOIN interaction_api_actions a ON a.request_id=c.request_id WHERE c.request_id=? AND c.run_id=? AND s.session_id=?`, request, run, session).Scan(&reservation, &ackFP, &raw)
	if err != nil {
		return
	}
	body, _ := json.Marshal(writeInput{RequestID: request})
	expected := fmt.Sprintf("%x", sha256.Sum256(append([]byte("/api/v1/runs/"+run+"/"+action), body...)))
	if reservation != expected {
		return out, interaction.ErrConflict
	}
	if !raw.Valid {
		return out, interaction.ErrReconciliation
	}
	if ackFP.String != expected {
		return out, interaction.ErrConflict
	}
	var a actionResult
	if json.Unmarshal([]byte(raw.String), &a) != nil || a.Status != 202 {
		return out, interaction.ErrReconciliation
	}
	var value struct {
		RunID string `json:"run_id"`
		State string `json:"state"`
		Phase string `json:"phase"`
	}
	if json.Unmarshal(a.Body, &value) != nil || value.RunID != run {
		return out, interaction.ErrReconciliation
	}
	return SkillRunControlReceipt{1, session, request, run, action, value.State, value.Phase}, nil
}
func skillRunRoute(p []string) bool { return len(p) >= 3 && p[0] == "sessions" && p[2] == "skill-runs" }

// POST callers hold the existing service mutex; reads never schedule work.
func (s *InteractionService) skillRuns(w http.ResponseWriter, r *http.Request, p []string) bool {
	if !skillRunRoute(p) {
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
	if r.Method == "GET" && len(p) == 3 {
		q := r.URL.Query()
		if !knowledgeQuery(q, "limit", "cursor") {
			wireError(w, 400, "invalid_query")
			return true
		}
		limit := 20
		if q.Has("limit") {
			limit, e = strconv.Atoi(q.Get("limit"))
			if e != nil || strconv.Itoa(limit) != q.Get("limit") || limit < 1 || limit > 100 {
				wireError(w, 400, "invalid_query")
				return true
			}
		}
		cursor := q.Get("cursor")
		if q.Has("cursor") && !skillWireIdentity(cursor) {
			wireError(w, 400, "invalid_query")
			return true
		}
		out, e = s.store.ListSkillRuns(ctx, p[1], cursor, limit)
	} else if r.URL.RawQuery != "" {
		wireError(w, 400, "invalid_query")
		return true
	} else if r.Method == "GET" && len(p) == 7 && p[3] == "controls" {
		out, e = s.skillControlReceipt(ctx, p[1], p[4], p[5], p[6])
	} else if r.Method == "GET" && len(p) == 5 && p[3] == "operations" {
		out, e = s.store.SkillRunOperation(ctx, p[1], p[4])
	} else if r.Method == "GET" && len(p) == 4 {
		out, e = s.store.SkillRun(ctx, p[1], p[3])
	} else if r.Method == "POST" && len(p) == 4 && (p[3] == "preview" || p[3] == "decision") {
		const cap = 6*32768 + 4096
		raw, err := io.ReadAll(io.LimitReader(r.Body, cap+1))
		if err != nil || len(raw) > cap {
			wireError(w, 400, "invalid_request")
			return true
		}
		if p[3] == "preview" {
			in, err := interaction.DecodeSkillRunIntent(raw)
			if err != nil || !skillWireIdentity(in.RequestID) || !skillWireIdentity(in.CatalogID) || !skillWireIdentity(in.RevisionID) {
				wireError(w, 400, "invalid_request")
				return true
			}
			out, e = s.store.PrepareSkillRun(ctx, p[1], in)
		} else {
			v, ok := operatorBody(raw, []string{"request_id", "operation_id", "digest", "decision"})
			if !ok || !skillWireIdentity(v["request_id"]) || !skillWireIdentity(v["operation_id"]) {
				wireError(w, 400, "invalid_request")
				return true
			}
			// A known replay consumes no extra worker and never redispatches. The native
			// ledger verifies its fingerprint; this read is not authority to admit.
			_, known := s.store.SkillRunOperation(ctx, p[1], v["request_id"])
			if known != nil && v["decision"] == "confirm" && len(s.workers) >= maxInteractionExecutors {
				wireError(w, 503, "service_capacity")
				return true
			}
			var op interaction.SkillRunOperation
			op, e = s.store.DecideSkillRun(ctx, p[1], interaction.OperatorDecision{RequestID: v["request_id"], OperationID: v["operation_id"], Digest: v["digest"], Decision: v["decision"]})
			out = op
			if e == nil && op.State == "admitted" && known != nil {
				run, err := s.store.GetRun(ctx, op.RunID)
				if err != nil {
					e = err
				} else {
					engine, err := s.engine(ctx, run, ProviderSelection{})
					if err != nil {
						e = err
					} else {
						s.start(run.ID, engine)
					}
				}
			}
		}
	} else {
		wireError(w, 404, "not_found")
		return true
	}
	switch {
	case errors.Is(e, interaction.ErrInvalid):
		wireError(w, 400, "invalid_request")
	case errors.Is(e, interaction.ErrConflict), errors.Is(e, interaction.ErrOwned):
		wireError(w, 409, "conflict")
	case errors.Is(e, interaction.ErrBudget):
		wireError(w, 409, "budget_exhausted")
	case errors.Is(e, interaction.ErrCapacity):
		wireError(w, 503, "service_capacity")
	case errors.Is(e, interaction.ErrReconciliation):
		wireError(w, 409, "needs_reconciliation")
	case errors.Is(e, sql.ErrNoRows):
		wireError(w, 404, "not_found")
	case e != nil:
		wireError(w, 500, "skill_run_outcome_unknown")
	default:
		wireJSON(w, 200, out)
	}
	return true
}
