package interaction

import (
	"context"
	"encoding/json"
)

// ChatOnlyPolicy authorizes history/context only, never a filesystem root or retrieval corpus.
type ChatOnlyPolicy struct {
	SystemConstraints []string
	UserConstraints   []string
}

func (s *Store) BindChatOnly(ctx context.Context, session string, p ChatOnlyPolicy) error {
	if session == "" {
		return ErrInvalid
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, "INSERT INTO interaction_chat_policies(session_id,policy) SELECT id,? FROM interaction_sessions WHERE id=?", string(raw), session)
	return changed(res, err)
}
func (s *Store) chatPolicy(ctx context.Context, session string) (ChatOnlyPolicy, error) {
	var p ChatOnlyPolicy
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT policy FROM interaction_chat_policies WHERE session_id=?", session).Scan(&raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &p)
	}
	return p, err
}
