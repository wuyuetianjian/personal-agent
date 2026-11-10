package interaction

import (
	"context"
	"database/sql"
	"encoding/json"

	"agent/internal/model"
)

type contextQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type admittedContextHistory struct {
	messages    []model.ChatMessage
	userSources map[int]string
	pendingID   string
	pending     Message
	latest      Message
}

// User authority comes from model-run admission, independently of provider/tool success.
// Use one ordered query snapshot for history, current input and retrieval query.
// Only completed checkpoints can contribute assistant/tool protocol; their
// injected context and duplicate original user message are excluded.
func (s *Store) admittedContext(ctx context.Context, sessionID string) (admittedContextHistory, error) {
	return admittedContextFrom(ctx, s.db, sessionID, "")
}
func admittedContextFrom(ctx context.Context, q contextQuery, sessionID, boundary string) (admittedContextHistory, error) {
	out := admittedContextHistory{userSources: map[int]string{}}
	var inherited string
	err := q.QueryRowContext(ctx, "SELECT transcript FROM interaction_session_branches WHERE session_id=?", sessionID).Scan(&inherited)
	if err == nil {
		var transcript savedTranscript
		if err = json.Unmarshal([]byte(inherited), &transcript); err != nil {
			return out, err
		}
		out.messages = transcript.Messages
		out.userSources = transcript.UserSources
		if out.userSources == nil {
			out.userSources = map[int]string{}
		}
	} else if err != sql.ErrNoRows {
		return out, err
	}
	if boundary != "" {
		var n int
		if err = q.QueryRowContext(ctx, "SELECT count(*) FROM interaction_runs WHERE id=? AND session_id=?", boundary, sessionID).Scan(&n); err != nil {
			return out, err
		}
		if n != 1 {
			return out, ErrInvalid
		}
	}
	rows, err := q.QueryContext(ctx, `SELECT r.id,r.state,r.message,e.checkpoint,r.execution_kind
 FROM interaction_runs r JOIN interaction_events v ON v.run_id=r.id AND v.type='run.created'
 LEFT JOIN interaction_engine e ON e.run_id=r.id
 WHERE r.session_id=? AND (?='' OR v.sequence<=(SELECT sequence FROM interaction_events WHERE run_id=? AND type='run.created')) ORDER BY v.sequence`, sessionID, boundary, boundary)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, raw, executionKind string
		var state RunState
		var checkpoint sql.NullString
		if err = rows.Scan(&id, &state, &raw, &checkpoint, &executionKind); err != nil {
			return out, err
		}
		// Run kind is immutable admission identity. Deterministic Skills retain
		// their evidence in the Skill/run/event ledgers, not in model messages.
		// Their synthetic input is neither a user restriction nor a retrieval
		// query or pending model turn. Keep the run-based boundary query above.
		switch executionKind {
		case skillRunAction:
			continue
		case "model":
			// Preserve the existing model transcript validation below.
		default:
			return out, ErrInvalid
		}
		var input Message
		if err = json.Unmarshal([]byte(raw), &input); err != nil {
			return out, err
		}
		out.latest = input
		if state == RunRunning {
			if out.pendingID != "" {
				return out, ErrConflict
			}
			out.pendingID = id
			out.pending = input
			continue
		}
		out.userSources[len(out.messages)] = "run:" + id
		out.messages = append(out.messages, model.ChatMessage{Role: "user", Content: input.Content})
		if state != RunCompleted || !checkpoint.Valid {
			continue
		}
		var cp Checkpoint
		if err = json.Unmarshal([]byte(checkpoint.String), &cp); err != nil {
			return out, err
		}
		if cp.MessageOffset < 0 || cp.MessageOffset >= len(cp.Messages) {
			return out, ErrInvalid
		}
		first := cp.Messages[cp.MessageOffset]
		if first.Role != "user" || first.Content != input.Content || len(first.ToolCalls) != 0 || first.ToolCallID != "" {
			return out, ErrInvalid
		}
		completed := cp.Messages[cp.MessageOffset+1:]
		for _, m := range completed {
			if m.Role != "assistant" && m.Role != "tool" {
				return out, ErrInvalid
			}
		}
		out.messages = append(out.messages, completed...)
	}
	return out, rows.Err()
}
