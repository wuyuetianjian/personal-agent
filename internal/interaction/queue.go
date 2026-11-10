package interaction

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const QueueSessionLimit = 16
const QueueGlobalLimit = 128

var ErrQueueRevoked = errors.New("queued original policy revoked")
var ErrQueueFull = errors.New("input queue full")

type QueueItem struct {
	ID            string `json:"id"`
	SessionID     string `json:"session_id"`
	RequestID     string `json:"request_id"`
	Content       string `json:"content"`
	State         string `json:"state"`
	RunID         string `json:"run_id"`
	Position      int    `json:"position"`
	Phase         string `json:"phase"`
	BlockedReason string `json:"blocked_reason"`
	PolicyDigest  string `json:"policy_digest"`
}
type QueueView struct {
	SchemaVersion int         `json:"schema_version"`
	SessionID     string      `json:"session_id"`
	Limit         int         `json:"limit"`
	Items         []QueueItem `json:"items"`
}
type QueueOperationRecord struct {
	SchemaVersion int       `json:"schema_version"`
	SessionID     string    `json:"session_id"`
	RequestID     string    `json:"request_id"`
	Kind          string    `json:"kind"`
	Item          QueueItem `json:"item"`
}

const queueColumns = `q.id,q.session_id,q.request_id,q.content,q.state,COALESCE(q.run_id,''),q.blocked_reason,q.policy,
 CASE WHEN q.state='pending' THEN (SELECT count(*) FROM interaction_queue p WHERE p.session_id=q.session_id AND p.state='pending' AND p.sequence<=q.sequence) ELSE 0 END,
 COALESCE((SELECT CASE WHEN r.state<>'running' THEN r.state ELSE COALESCE(e.phase,'unknown_admission') END FROM interaction_runs r LEFT JOIN interaction_engine e ON e.run_id=r.id WHERE r.id=q.run_id),'')`

func scanQueue(row scanner) (QueueItem, error) {
	var q QueueItem
	var policy string
	e := row.Scan(&q.ID, &q.SessionID, &q.RequestID, &q.Content, &q.State, &q.RunID, &q.BlockedReason, &policy, &q.Position, &q.Phase)
	q.PolicyDigest = fmt.Sprintf("%x", sha256.Sum256([]byte(policy)))
	return q, e
}

// Serializes only already-bound authority, never a live resolver's replacement.
func queuePolicy(ctx context.Context, tx *sql.Tx, session string) (string, error) {
	var binding, mode, workspace, chat, identity string
	e := tx.QueryRowContext(ctx, `SELECT a.binding,a.mode,COALESCE(w.policy,''),COALESCE(c.policy,''),COALESCE(w.root_identity,'') FROM interaction_api_sessions a LEFT JOIN interaction_workspaces w ON w.session_id=a.session_id LEFT JOIN interaction_chat_policies c ON c.session_id=a.session_id WHERE a.session_id=?`, session).Scan(&binding, &mode, &workspace, &chat, &identity)
	if e != nil {
		return "", e
	}
	if workspace == "" && chat == "" {
		return "", ErrInvalid
	}
	raw, e := json.Marshal([]string{binding, mode, workspace, chat, identity})
	return string(raw), e
}
func (s *Store) queueWrite(ctx context.Context, session string) (*sql.Tx, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	res, e := tx.ExecContext(ctx, "UPDATE interaction_sessions SET sequence=sequence WHERE id=?", session)
	if e = changed(res, e); e != nil {
		tx.Rollback()
		return nil, e
	}
	return tx, nil
}
func (s *Store) Enqueue(ctx context.Context, session, request string, message Message) (QueueItem, error) {
	if request == "" || len(request) > 128 || strings.TrimSpace(message.Content) == "" || len(message.Content) > 64<<10 {
		return QueueItem{}, ErrInvalid
	}
	tx, e := s.queueWrite(ctx, session)
	if e != nil {
		return QueueItem{}, e
	}
	defer tx.Rollback()
	var owner, kind, item, content string
	e = tx.QueryRowContext(ctx, "SELECT session_id,kind,item_id,content FROM interaction_queue_operations WHERE request_id=?", request).Scan(&owner, &kind, &item, &content)
	if e == nil {
		if owner != session || kind != "enqueue" || content != message.Content {
			return QueueItem{}, ErrConflict
		}
		q, e := scanQueue(tx.QueryRowContext(ctx, "SELECT "+queueColumns+" FROM interaction_queue q WHERE q.id=?", item))
		if e != nil {
			return q, e
		}
		return q, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return QueueItem{}, e
	}
	var local, total int
	e = tx.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum(session_id=?),0) FROM interaction_queue WHERE state='pending'", session).Scan(&total, &local)
	if e != nil {
		return QueueItem{}, e
	}
	if local >= QueueSessionLimit || total >= QueueGlobalLimit {
		return QueueItem{}, ErrQueueFull
	}
	policy, e := queuePolicy(ctx, tx, session)
	if e != nil {
		return QueueItem{}, e
	}
	id := uuid.NewString()
	if _, e = tx.ExecContext(ctx, "INSERT INTO interaction_queue(id,session_id,request_id,content,policy,state) VALUES(?,?,?,?,?,'pending')", id, session, request, message.Content, policy); e != nil {
		return QueueItem{}, e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO interaction_queue_operations(request_id,session_id,kind,item_id,content) VALUES(?,?,'enqueue',?,?)", request, session, id, message.Content); e != nil {
		return QueueItem{}, e
	}
	q, e := scanQueue(tx.QueryRowContext(ctx, "SELECT "+queueColumns+" FROM interaction_queue q WHERE q.id=?", id))
	if e != nil {
		return q, e
	}
	return q, tx.Commit()
}
func (s *Store) WithdrawQueue(ctx context.Context, session, id, request string) (QueueItem, error) {
	if request == "" || len(request) > 128 {
		return QueueItem{}, ErrInvalid
	}
	tx, e := s.queueWrite(ctx, session)
	if e != nil {
		return QueueItem{}, e
	}
	defer tx.Rollback()
	var owner, kind, item string
	e = tx.QueryRowContext(ctx, "SELECT session_id,kind,item_id FROM interaction_queue_operations WHERE request_id=?", request).Scan(&owner, &kind, &item)
	if e == nil {
		if owner != session || kind != "withdraw" || item != id {
			return QueueItem{}, ErrConflict
		}
	} else if errors.Is(e, sql.ErrNoRows) {
		res, err := tx.ExecContext(ctx, "UPDATE interaction_queue SET state='withdrawn',blocked_reason='' WHERE id=? AND session_id=? AND state='pending'", id, session)
		if err = changed(res, err); err != nil {
			return QueueItem{}, err
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO interaction_queue_operations(request_id,session_id,kind,item_id,content) VALUES(?,?,'withdraw',?,'')", request, session, id); e != nil {
			return QueueItem{}, e
		}
	} else {
		return QueueItem{}, e
	}
	q, e := scanQueue(tx.QueryRowContext(ctx, "SELECT "+queueColumns+" FROM interaction_queue q WHERE q.id=?", id))
	if e != nil {
		return q, e
	}
	return q, tx.Commit()
}

// Admission and run.created commit together. A successful call is a single-use
// claim. Any ambiguous commit is inspected, never re-executed by the queue.
func (s *Store) AdmitQueue(ctx context.Context, id string) (Run, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Run{}, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "UPDATE interaction_queue SET blocked_reason=blocked_reason WHERE id=? AND state='pending'", id); e != nil {
		return Run{}, e
	}
	var session, request, content, policy string
	e = tx.QueryRowContext(ctx, `SELECT session_id,request_id,content,policy FROM interaction_queue q WHERE id=? AND state='pending' AND blocked_reason='' AND NOT EXISTS(SELECT 1 FROM interaction_queue p WHERE p.session_id=q.session_id AND p.state='pending' AND p.sequence<q.sequence) AND NOT EXISTS(SELECT 1 FROM interaction_runs r WHERE r.session_id=q.session_id AND r.state='running')`, id).Scan(&session, &request, &content, &policy)
	if errors.Is(e, sql.ErrNoRows) {
		return Run{}, ErrConflict
	}
	if e != nil {
		return Run{}, e
	}
	current, e := queuePolicy(ctx, tx, session)
	if e != nil {
		return Run{}, e
	}
	if current != policy {
		return Run{}, ErrConflict
	}
	var rootPolicy, rootID string
	e = tx.QueryRowContext(ctx, "SELECT policy,root_identity FROM interaction_workspaces WHERE session_id=?", session).Scan(&rootPolicy, &rootID)
	if e == nil {
		var p WorkspacePolicy
		if json.Unmarshal([]byte(rootPolicy), &p) != nil {
			return Run{}, ErrQueueRevoked
		}
		identity, err := workspaceIdentity(p.Root)
		if err != nil || identity != rootID {
			return Run{}, ErrQueueRevoked
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		return Run{}, e
	}
	r := Run{ID: uuid.NewString(), SessionID: session, RequestID: "queue:" + id, Message: Message{Content: content}, State: RunRunning, Version: 1}
	raw, _ := json.Marshal(r.Message)
	if _, e = tx.ExecContext(ctx, "INSERT INTO interaction_runs("+runColumns+") VALUES(?,?,?,?,?,?)", r.ID, session, r.RequestID, string(raw), r.State, r.Version); e != nil {
		return Run{}, e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE interaction_queue SET state='admitted',run_id=? WHERE id=? AND state='pending'", r.ID, id); e != nil {
		return Run{}, e
	}
	if e = appendEvent(ctx, tx, r, "run.created"); e != nil {
		return Run{}, e
	}
	return r, tx.Commit()
}
func (s *Store) Queue(ctx context.Context, session string) (QueueView, error) {
	v := QueueView{SchemaVersion: 1, SessionID: session, Limit: QueueSessionLimit, Items: []QueueItem{}}
	// Bounded inspection includes all pending plus the most recent 50 terminal items.
	rows, e := s.db.QueryContext(ctx, "SELECT "+queueColumns+" FROM interaction_queue q WHERE q.session_id=? AND (q.state='pending' OR q.sequence IN (SELECT sequence FROM interaction_queue WHERE session_id=? AND state<>'pending' ORDER BY sequence DESC LIMIT 50)) ORDER BY q.sequence", session, session)
	if e != nil {
		return v, e
	}
	defer rows.Close()
	for rows.Next() {
		q, e := scanQueue(rows)
		if e != nil {
			return v, e
		}
		v.Items = append(v.Items, q)
	}
	if e = rows.Err(); e != nil {
		return v, e
	}
	rows.Close()
	var phase string
	e = s.db.QueryRowContext(ctx, "SELECT COALESCE(e.phase,'unknown_admission') FROM interaction_runs r LEFT JOIN interaction_engine e ON e.run_id=r.id WHERE r.session_id=? AND r.state='running' LIMIT 1", session).Scan(&phase)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return v, e
	}
	for i := range v.Items {
		q := &v.Items[i]
		if q.State == "pending" && q.BlockedReason == "" {
			if phase != "" {
				q.BlockedReason = "previous_run:" + phase
			} else if q.Position > 1 {
				q.BlockedReason = "fifo_wait"
			} else {
				q.BlockedReason = "waiting_capacity"
			}
		}
	}
	return v, nil
}
func (s *Store) QueueOperation(ctx context.Context, session, request string) (QueueOperationRecord, error) {
	v := QueueOperationRecord{SchemaVersion: 1, SessionID: session, RequestID: request}
	var id string
	e := s.db.QueryRowContext(ctx, "SELECT kind,item_id FROM interaction_queue_operations WHERE session_id=? AND request_id=?", session, request).Scan(&v.Kind, &id)
	if e != nil {
		return v, e
	}
	v.Item, e = scanQueue(s.db.QueryRowContext(ctx, "SELECT "+queueColumns+" FROM interaction_queue q WHERE q.id=? AND q.session_id=?", id, session))
	return v, e
}
func (s *Store) PendingQueueHeads(ctx context.Context) ([]QueueItem, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT "+queueColumns+" FROM interaction_queue q WHERE q.state='pending' AND q.blocked_reason='' AND NOT EXISTS(SELECT 1 FROM interaction_queue p WHERE p.session_id=q.session_id AND p.state='pending' AND p.sequence<q.sequence) AND NOT EXISTS(SELECT 1 FROM interaction_runs r WHERE r.session_id=q.session_id AND r.state='running') ORDER BY q.sequence LIMIT ?", QueueGlobalLimit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []QueueItem{}
	for rows.Next() {
		q, e := scanQueue(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, q)
	}
	return items, rows.Err()
}

// Revocation/preflight failure is sticky; restoring a provider never silently
// reinterprets old intent. Withdraw and explicitly enqueue a new item instead.
func (s *Store) BlockQueue(ctx context.Context, id, reason string) error {
	res, e := s.db.ExecContext(ctx, "UPDATE interaction_queue SET blocked_reason=? WHERE id=? AND state='pending'", reason, id)
	return changed(res, e)
}
