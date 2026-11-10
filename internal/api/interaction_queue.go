package api

import (
	"agent/internal/interaction"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

func queueRoute(p []string) bool {
	return len(p) == 3 && p[0] == "sessions" && p[2] == "queue" || len(p) == 5 && p[0] == "sessions" && p[2] == "queue" && p[4] == "withdraw"
}
func (s *InteractionService) readQueue(w http.ResponseWriter, r *http.Request, p []string) bool {
	if !(len(p) == 3 && p[0] == "sessions" && p[2] == "queue" || len(p) >= 4 && p[0] == "sessions" && p[2] == "queue-operations") {
		return false
	}
	if _, _, e := s.session(r.Context(), p[1]); e != nil {
		wireError(w, 404, "session_not_found")
		return true
	}
	if len(p) == 3 {
		v, e := s.store.Queue(r.Context(), p[1])
		if e != nil {
			wireError(w, 500, "storage_error")
		} else {
			wireJSON(w, 200, v)
		}
	} else {
		request, e := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), "/api/v1/sessions/"+url.PathEscape(p[1])+"/queue-operations/"))
		if e != nil {
			wireError(w, 400, "invalid_request")
			return true
		}
		v, e := s.store.QueueOperation(r.Context(), p[1], request)
		if errors.Is(e, sql.ErrNoRows) {
			wireError(w, 404, "queue_operation_not_found")
		} else if e != nil {
			wireError(w, 500, "storage_error")
		} else {
			wireJSON(w, 200, v)
		}
	}
	return true
}

// Go's existing service worker registry remains the sole executor. This scans
// only never-admitted intents, under the same mutex used for ordinary writes.
// No timer/lease/workflow worker can claim these runs a second time.
func (s *InteractionService) kickQueues() {
	if s.closed {
		return
	}
	items, e := s.store.PendingQueueHeads(s.ctx)
	if e != nil {
		return
	}
	for _, q := range items {
		if len(s.workers) >= maxInteractionExecutors {
			return
		}
		sel, e := s.resolveSession(s.ctx, q.SessionID)
		if e != nil {
			_ = s.store.BlockQueue(s.ctx, q.ID, "provider_unavailable_or_changed: withdraw and enqueue with new intent")
			continue
		}
		if bound, err := s.store.Workspace(s.ctx, q.SessionID); err == nil && bound.Policy.NonSandboxExec && bound.Policy.Mode != interaction.ModePlan && !s.broker.Capabilities().Exec.Enabled {
			_ = s.store.BlockQueue(s.ctx, q.ID, "exec_host_authority_revoked: withdraw; new explicit intent required")
			continue
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			continue
		}
		revoked := false
		for _, server := range s.broker.MCPStatus(s.ctx, q.SessionID).Servers {
			if server.State == "disconnected" {
				revoked = true
			}
		}
		if revoked {
			_ = s.store.BlockQueue(s.ctx, q.ID, "mcp_generation_revoked: withdraw; create a new explicitly opted-in session")
			continue
		}
		// Validate the unchanged root/context before consuming the queue claim.
		if _, e = s.modelEngine(s.ctx, interaction.Run{ID: "queue-preflight:" + q.ID, SessionID: q.SessionID}, sel); e != nil {
			_ = s.store.BlockQueue(s.ctx, q.ID, "context_or_scope_blocked: withdraw and inspect original policy")
			continue
		}
		run, e := s.store.AdmitQueue(s.ctx, q.ID)
		if e != nil {
			if errors.Is(e, interaction.ErrQueueRevoked) {
				_ = s.store.BlockQueue(s.ctx, q.ID, "original_workspace_revoked: withdraw; new explicit intent required")
			}
			continue
		} // ambiguous commit NEVER starts a worker
		engine, e := s.engine(s.ctx, run, sel)
		if e != nil {
			// Keep claimed run unresolved. It may not be silently reinterpreted or
			// skipped after an admission/context race or storage failure.
			continue
		}
		s.start(run.ID, engine)
	}
}
func (s *InteractionService) writeQueue(ctx context.Context, w http.ResponseWriter, p []string, in writeInput) {
	var e error
	if len(p) == 3 {
		_, e = s.store.Enqueue(ctx, p[1], in.RequestID, interaction.Message{Content: in.Content})
	} else {
		_, e = s.store.WithdrawQueue(ctx, p[1], p[3], in.RequestID)
	}
	if e != nil {
		switch {
		case errors.Is(e, interaction.ErrInvalid), errors.Is(e, interaction.ErrConflict), errors.Is(e, interaction.ErrQueueFull), errors.Is(e, sql.ErrNoRows):
			wireError(w, 409, "queue_operation_rejected")
		default:
			wireError(w, 500, "outcome_unknown_check_queue")
		}
		return
	}
	// The queue's transactional operation ledger, not the generic HTTP cache,
	// proves acknowledgement. Reads/retries cannot claim an admitted item.
	s.kickQueues()
	record, e := s.store.QueueOperation(ctx, p[1], in.RequestID)
	if e != nil {
		wireError(w, 500, "outcome_unknown_check_queue")
		return
	}
	status := 202
	if len(p) == 5 {
		status = 200
	}
	wireJSON(w, status, record)
}
