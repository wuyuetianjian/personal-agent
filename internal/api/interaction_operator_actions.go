package api

import (
	"agent/internal/interaction"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
	"unicode/utf8"
)

// Management metadata needs the immutable stored binding, not a live provider,
// MCP status or session capability projection. Recovery must work independently
// of later provider configuration/credential availability.
func (s *InteractionService) operatorStoredSession(ctx context.Context, id string) error {
	var raw string
	if e := s.runtime.DB.SQL.QueryRowContext(ctx, "SELECT binding FROM interaction_api_sessions WHERE session_id=?", id).Scan(&raw); e != nil {
		return e
	}
	var binding sessionBinding
	return json.Unmarshal([]byte(raw), &binding)
}

func operatorActionRoute(p []string) bool {
	return len(p) == 4 && p[0] == "sessions" && p[2] == "operator-actions"
}
func (s *InteractionService) operatorActions(w http.ResponseWriter, r *http.Request, p []string) bool {
	if !operatorActionRoute(p) {
		return false
	}
	if r.URL.RawQuery != "" {
		wireError(w, 400, "invalid_query")
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if e := s.operatorStoredSession(ctx, p[1]); e != nil {
		wireError(w, 404, "session_not_found")
		return true
	}
	var out interaction.OperatorOperation
	var e error
	if r.Method == "GET" {
		out, e = s.store.OperatorOperation(ctx, p[1], p[3])
	} else {
		allowed := []string{}
		switch p[3] {
		case "preview":
			allowed = []string{"request_id", "kind", "target_id", "new_value"}
		case "decision":
			allowed = []string{"request_id", "operation_id", "digest", "decision"}
		default:
			wireError(w, 404, "not_found")
			return true
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 8193))
		if err != nil || len(body) > 8192 {
			wireError(w, 400, "invalid_request")
			return true
		}
		values, ok := operatorBody(body, allowed)
		if !ok {
			wireError(w, 400, "invalid_request")
			return true
		}
		if p[3] == "preview" {
			out, e = s.store.PrepareOperator(ctx, p[1], interaction.OperatorIntent{RequestID: values["request_id"], Kind: values["kind"], TargetID: values["target_id"], NewValue: values["new_value"]})
		} else {
			out, e = s.store.DecideOperator(ctx, p[1], interaction.OperatorDecision{RequestID: values["request_id"], OperationID: values["operation_id"], Digest: values["digest"], Decision: values["decision"]})
		}
	}
	switch {
	case errors.Is(e, interaction.ErrInvalid):
		wireError(w, 400, "operator_invalid_request")
	case errors.Is(e, interaction.ErrConflict):
		wireError(w, 409, "operator_request_conflict")
	case errors.Is(e, sql.ErrNoRows):
		wireError(w, 404, "operator_not_found")
	case e != nil:
		wireError(w, 500, "operator_outcome_unknown")
	default:
		wireJSON(w, 200, out)
	}
	return true
}

// All four flat fields are required strings. Reject nulls, duplicates, unknown
// keys and trailing JSON rather than silently choosing one interpretation.
func operatorBody(raw []byte, allowed []string) (map[string]string, bool) {
	if !utf8.Valid(raw) || !knowledgeUnicodeJSON(raw) {
		return nil, false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return nil, false
	}
	out := map[string]string{}
	for d.More() {
		t, e = d.Token()
		key, ok := t.(string)
		if e != nil || !ok {
			return nil, false
		}
		if _, exists := out[key]; exists {
			return nil, false
		}
		valid := false
		for _, k := range allowed {
			if k == key {
				valid = true
			}
		}
		if !valid {
			return nil, false
		}
		var value any
		if e = d.Decode(&value); e != nil {
			return nil, false
		}
		v, ok := value.(string)
		if !ok {
			return nil, false
		}
		out[key] = v
	}
	if _, e = d.Token(); e != nil {
		return nil, false
	}
	if _, e = d.Token(); e != io.EOF || len(out) != len(allowed) {
		return nil, false
	}
	return out, true
}
