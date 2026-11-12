//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_lease && nativeprobe_sqlite_permit && nativeprobe_sqlite_persistent && nativeprobe_sqlite_main_read && nativeprobe_sqlite_existing_wal && nativeprobe_sqlite_shm_resources && nativeprobe_sqlite_shm_slots && nativeprobe_sqlite_private_sql

package windowsprobe

import (
	"context"
	"database/sql"
	"errors"
	_ "modernc.org/sqlite"
	"sync"
)

// RED session scaffold. The paired VFS always refuses actual main xOpen; no SQL
// success is possible. Before GREEN, replace these forwarding bodies with real
// connection/Rows/Stmt core-pin ownership and close/drain synchronization.
type privateSQLSession struct {
	mu           sync.Mutex
	db           *sql.DB
	conn         *sql.Conn
	closing      bool
	closeStorage func() error
}
type privateSQLRows struct {
	mu   sync.Mutex
	rows *sql.Rows
}
type privateSQLStmt struct{ stmt *sql.Stmt }

func newPrivateSQLSession(uri string, closeStorage func() error) (*privateSQLSession, error) {
	db, e := sql.Open("sqlite", uri)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	return &privateSQLSession{db: db, closeStorage: closeStorage}, nil
}
func (s *privateSQLSession) connection(ctx context.Context) (*sql.Conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return nil, errPersistentPermitClosed
	}
	if s.conn == nil {
		c, e := s.db.Conn(ctx)
		if e != nil {
			return nil, e
		}
		s.conn = c
	}
	return s.conn, nil
}
func (s *privateSQLSession) query(ctx context.Context, q string) (*privateSQLRows, error) {
	c, e := s.connection(ctx)
	if e != nil {
		return nil, e
	}
	r, e := c.QueryContext(ctx, q)
	if e != nil {
		return nil, e
	}
	return &privateSQLRows{rows: r}, nil
}
func (s *privateSQLSession) prepare(ctx context.Context, q string) (*privateSQLStmt, error) {
	c, e := s.connection(ctx)
	if e != nil {
		return nil, e
	}
	p, e := c.PrepareContext(ctx, q)
	if e != nil {
		return nil, e
	}
	return &privateSQLStmt{p}, nil
}
func (s *privateSQLSession) exec(ctx context.Context, q string) error {
	c, e := s.connection(ctx)
	if e != nil {
		return e
	}
	_, e = c.ExecContext(ctx, q)
	return e
}
func (s *privateSQLSession) close(ctx context.Context) error {
	s.mu.Lock()
	s.closing = true
	c := s.conn
	s.conn = nil
	s.mu.Unlock()
	var e error
	if c != nil {
		e = c.Close()
	}
	e = errors.Join(e, s.db.Close())
	if s.closeStorage != nil {
		e = errors.Join(e, s.closeStorage())
	}
	return e
}
func (r *privateSQLRows) next() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.rows.Next() }
func (r *privateSQLRows) scan(v ...any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rows.Scan(v...)
}
func (r *privateSQLRows) err() error   { r.mu.Lock(); defer r.mu.Unlock(); return r.rows.Err() }
func (r *privateSQLRows) close() error { r.mu.Lock(); defer r.mu.Unlock(); return r.rows.Close() }
func (p *privateSQLStmt) query(ctx context.Context) (*privateSQLRows, error) {
	r, e := p.stmt.QueryContext(ctx)
	if e != nil {
		return nil, e
	}
	return &privateSQLRows{rows: r}, nil
}
func (p *privateSQLStmt) close() error { return p.stmt.Close() }
