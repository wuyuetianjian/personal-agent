package interaction

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"time"
)

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }
func (s *Store) CreateSession(ctx context.Context, id string) error {
	if id == "" {
		return ErrInvalid
	}
	_, e := s.db.ExecContext(ctx, "INSERT INTO interaction_sessions(id) VALUES (?)", id)
	return e
}

type scanner interface{ Scan(...any) error }

func scanRun(row scanner) (Run, error) {
	var r Run
	var msg string
	e := row.Scan(&r.ID, &r.SessionID, &r.RequestID, &msg, &r.State, &r.Version)
	if e == nil {
		e = json.Unmarshal([]byte(msg), &r.Message)
	}
	return r, e
}

const runColumns = "id,session_id,request_id,message,state,version"

func (s *Store) GetRun(ctx context.Context, id string) (Run, error) {
	return scanRun(s.db.QueryRowContext(ctx, "SELECT "+runColumns+" FROM interaction_runs WHERE id=?", id))
}
func appendEvent(ctx context.Context, tx *sql.Tx, r Run, kind string) error {
	var executionKind string
	if err := tx.QueryRowContext(ctx, `SELECT execution_kind FROM interaction_runs WHERE id=?`, r.ID).Scan(&executionKind); err != nil {
		return err
	}
	if executionKind == skillRunAction {
		return appendEngineEvent(ctx, tx, r, kind, r)
	}

	_, e := tx.ExecContext(ctx, "UPDATE interaction_sessions SET sequence=sequence+1 WHERE id=?", r.SessionID)
	if e != nil {
		return e
	}
	payload, e := json.Marshal(r)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, "INSERT INTO interaction_events(session_id,run_id,sequence,version,type,payload) SELECT id,?,sequence,?,?,? FROM interaction_sessions WHERE id=?", r.ID, r.Version, kind, string(payload), r.SessionID)
	return e
}
func (s *Store) CreateRun(ctx context.Context, sessionID, requestID string, message Message) (Run, error) {
	if sessionID == "" || requestID == "" {
		return Run{}, ErrInvalid
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Run{}, e
	}
	defer tx.Rollback()
	// Acquire the SQLite writer before reading to avoid deferred transaction upgrades.
	res, e := tx.ExecContext(ctx, "UPDATE interaction_sessions SET sequence=sequence WHERE id=?", sessionID)
	if e != nil {
		return Run{}, e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return Run{}, e
	}
	if n != 1 {
		return Run{}, sql.ErrNoRows
	}
	existing, e := scanRun(tx.QueryRowContext(ctx, "SELECT "+runColumns+" FROM interaction_runs WHERE session_id=? AND request_id=?", sessionID, requestID))
	if e == nil {
		return existing, tx.Commit()
	}
	if e != sql.ErrNoRows {
		return Run{}, e
	}
	var count int
	if e = tx.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM interaction_runs WHERE session_id=? AND state='running') + (SELECT count(*) FROM interaction_queue WHERE session_id=? AND state='pending')", sessionID, sessionID).Scan(&count); e != nil {
		return Run{}, e
	}
	if count != 0 {
		return Run{}, ErrConflict
	}
	r := Run{ID: uuid.NewString(), SessionID: sessionID, RequestID: requestID, Message: message, State: RunRunning, Version: 1}
	msg, _ := json.Marshal(message)
	_, e = tx.ExecContext(ctx, "INSERT INTO interaction_runs("+runColumns+") VALUES (?,?,?,?,?,?)", r.ID, sessionID, requestID, string(msg), r.State, r.Version)
	if e != nil {
		return Run{}, e
	}
	if e = appendEvent(ctx, tx, r, "run.created"); e != nil {
		return Run{}, e
	}
	return r, tx.Commit()
}
func (s *Store) Transition(ctx context.Context, id string, version int64, next RunState) (Run, error) {
	if next != RunCompleted && next != RunCancelled && next != RunFailed {
		return Run{}, ErrInvalid
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Run{}, e
	}
	defer tx.Rollback()
	res, e := tx.ExecContext(ctx, "UPDATE interaction_runs SET state=?,version=version+1 WHERE id=? AND version=? AND state='running'", next, id, version)
	if e != nil {
		return Run{}, e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return Run{}, e
	}
	if n != 1 {
		return Run{}, ErrConflict
	}
	r, e := scanRun(tx.QueryRowContext(ctx, "SELECT "+runColumns+" FROM interaction_runs WHERE id=?", id))
	if e != nil {
		return Run{}, e
	}
	if e = appendEvent(ctx, tx, r, "run."+string(next)); e != nil {
		return Run{}, e
	}
	return r, tx.Commit()
}
func (s *Store) Events(ctx context.Context, sessionID string, after int64, limit int) ([]Event, error) {
	if after < 0 || limit < 1 || limit > 1000 {
		return nil, ErrInvalid
	}
	rows, e := s.db.QueryContext(ctx, "SELECT version,session_id,run_id,sequence,type,payload FROM interaction_events WHERE session_id=? AND sequence>? ORDER BY sequence LIMIT ?", sessionID, after, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var ev Event
		var p string
		if e = rows.Scan(&ev.Version, &ev.SessionID, &ev.RunID, &ev.Sequence, &ev.Type, &p); e != nil {
			return nil, fmt.Errorf("scan event: %w", e)
		}
		ev.Payload = json.RawMessage(p)
		events = append(events, ev)
	}
	return events, rows.Err()
}

// Approval binding and expiry are immutable; decision and consumption are one-way CAS operations.
func (s *Store) CreateApproval(ctx context.Context, a Approval) error {
	if a.ID == "" || a.RunID == "" || a.ActionDigest == "" || a.ExpiresAt.IsZero() || a.Decision != "" || a.ConsumedAt != nil {
		return ErrInvalid
	}
	res, e := s.db.ExecContext(ctx, "INSERT INTO interaction_approvals(id,run_id,action_digest,expires_at) SELECT ?,id,?,? FROM interaction_runs WHERE id=? AND state='running'", a.ID, a.ActionDigest, a.ExpiresAt.UnixNano(), a.RunID)
	return changed(res, e)
}
func changed(res sql.Result, e error) error {
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (s *Store) DecideApproval(ctx context.Context, id, digest, decision string) error {
	if decision != "approved" && decision != "denied" {
		return ErrInvalid
	}
	tx, now, e := s.approvalWrite(ctx, id)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	res, e := tx.ExecContext(ctx, "UPDATE interaction_approvals SET decision=? WHERE id=? AND action_digest=? AND decision='pending' AND expires_at>? AND EXISTS(SELECT 1 FROM interaction_runs WHERE id=run_id AND state='running') AND NOT EXISTS(SELECT 1 FROM interaction_engine WHERE run_id=interaction_approvals.run_id AND cancel_requested=1)", decision, id, digest, now)
	if e = changed(res, e); e != nil {
		return e
	}
	var runID, sessionID string
	var version int64
	if e = tx.QueryRowContext(ctx, "SELECT r.id,r.session_id,r.version FROM interaction_runs r JOIN interaction_approvals a ON a.run_id=r.id WHERE a.id=?", id).Scan(&runID, &sessionID, &version); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE interaction_sessions SET sequence=sequence+1 WHERE id=?", sessionID); e != nil {
		return e
	}
	payload, _ := json.Marshal(map[string]string{"id": id, "digest": digest, "decision": decision})
	if _, e = tx.ExecContext(ctx, "INSERT INTO interaction_events(session_id,run_id,sequence,version,type,payload) SELECT id,?,sequence,?,'approval.decided',? FROM interaction_sessions WHERE id=?", runID, version, string(payload), sessionID); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) ConsumeApproval(ctx context.Context, id, digest string) error {
	tx, now, e := s.approvalWrite(ctx, id)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	res, e := tx.ExecContext(ctx, "UPDATE interaction_approvals SET consumed_at=? WHERE id=? AND action_digest=? AND decision='approved' AND consumed_at IS NULL AND expires_at>? AND EXISTS(SELECT 1 FROM interaction_runs WHERE id=run_id AND state='running') AND NOT EXISTS(SELECT 1 FROM interaction_engine WHERE run_id=interaction_approvals.run_id AND cancel_requested=1)", now, id, digest, now)
	if e = changed(res, e); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) GetApproval(ctx context.Context, id string) (Approval, error) {
	var a Approval
	var expiry int64
	var consumed sql.NullInt64
	e := s.db.QueryRowContext(ctx, "SELECT id,run_id,action_digest,expires_at,decision,consumed_at FROM interaction_approvals WHERE id=?", id).Scan(&a.ID, &a.RunID, &a.ActionDigest, &expiry, &a.Decision, &consumed)
	if e != nil {
		return a, e
	}
	a.ExpiresAt = time.Unix(0, expiry).UTC()
	if consumed.Valid {
		v := time.Unix(0, consumed.Int64).UTC()
		a.ConsumedAt = &v
	}
	return a, nil
}

// approvalWrite first waits for a connection and SQLite's writer reservation.
// Only then may expiry be sampled; a queued caller must not use an old clock.
func (s *Store) approvalWrite(ctx context.Context, id string) (*sql.Tx, int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE interaction_approvals SET consumed_at=consumed_at WHERE id=?", id); err != nil {
		tx.Rollback()
		return nil, 0, err
	}
	return tx, time.Now().UnixNano(), nil
}

// RecordCapacityWait publishes scheduling status only. It does not acquire an
// execution owner or authorize retry of an owned/uncertain operation.
func (s *Store) RecordCapacityWait(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE interaction_runs SET version=version WHERE id=?", id); err != nil {
		return err
	}
	r, err := scanRun(tx.QueryRowContext(ctx, "SELECT "+runColumns+" FROM interaction_runs WHERE id=?", id))
	if err != nil {
		return err
	}
	if r.State != RunRunning {
		return ErrConflict
	}
	var owned int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM interaction_engine WHERE run_id=? AND owner<>''", id).Scan(&owned); err != nil {
		return err
	}
	if owned != 0 {
		return ErrOwned
	}
	if err = appendEvent(ctx, tx, r, "run.waiting_capacity"); err != nil {
		return err
	}
	return tx.Commit()
}
