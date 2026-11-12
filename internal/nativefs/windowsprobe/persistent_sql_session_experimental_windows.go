//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_lease && nativeprobe_sqlite_permit && nativeprobe_sqlite_persistent && nativeprobe_sqlite_main_read && nativeprobe_sqlite_existing_wal && nativeprobe_sqlite_shm_resources && nativeprobe_sqlite_shm_slots && nativeprobe_sqlite_private_sql

package windowsprobe

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	driver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
	"sync"
)

// Trusted fixed-URI experiment. Native calls intentionally have no asynchronous
// context cancellation: database/sql's awaitDone would bypass coreMu. Context is
// checked at admission; Close(ctx) bounds waiting, never native destruction.
type privateSQLSession struct {
	mu                          sync.Mutex
	coreMu                      sync.Mutex
	db                          *sql.DB
	conn                        *sql.Conn
	closing, finalizing, closed bool
	active, objects             int
	terminal, closeResult       error
	changed                     chan struct{}
	pin                         func() (*persistentSHMTestTicket, error)
	closeStorage                func() error
	verifyRetired               func() error
	beforeSnapshot              func() // trusted test handshake, under coreMu

	ownerPin        *persistentOperationPermit
	beforeRowsClose func() error
	beforeStmtClose func() error
	healthy         func() error
}
type privateSQLRows struct {
	mu     sync.Mutex
	s      *privateSQLSession
	rows   *sql.Rows
	ticket *persistentSHMTestTicket
	parent *privateSQLStmt
	done   bool
	result error
}
type privateSQLStmt struct {
	s        *privateSQLSession
	stmt     *sql.Stmt
	ticket   *persistentSHMTestTicket
	children int // coreMu protected; Close cannot finalize active Rows.
	done     bool
	result   error
}

func newPrivateSQLSession(uri string, connection *persistentPermitConnection, pin func() (*persistentSHMTestTicket, error), verifyRetired, healthy, closeStorage func() error) (*privateSQLSession, error) {
	if connection == nil || pin == nil || verifyRetired == nil || healthy == nil || closeStorage == nil {
		return nil, errors.New("missing SQL ownership callbacks")
	}
	db, e := sql.Open("sqlite", uri)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	ownerPin, e := connection.begin(context.Background())
	if e != nil {
		return nil, errors.Join(e, db.Close())
	}
	return &privateSQLSession{healthy: healthy, ownerPin: ownerPin, db: db, pin: pin, verifyRetired: verifyRetired, closeStorage: closeStorage, changed: make(chan struct{})}, nil
}
func (s *privateSQLSession) signalLocked() { close(s.changed); s.changed = make(chan struct{}) }
func (s *privateSQLSession) poison(e error) {
	s.mu.Lock()
	s.terminal = errors.Join(s.terminal, e)
	s.signalLocked()
	s.mu.Unlock()
}
func (s *privateSQLSession) begin(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.terminal != nil {
		return errors.Join(errPersistentPermitClosed, s.terminal)
	}
	s.active++
	return nil
}
func (s *privateSQLSession) end() { s.mu.Lock(); s.active--; s.signalLocked(); s.mu.Unlock() }
func (s *privateSQLSession) owned(delta int) {
	s.mu.Lock()
	s.objects += delta
	s.signalLocked()
	s.mu.Unlock()
}
func (s *privateSQLSession) connection(ctx context.Context) (*sql.Conn, error) {
	if s.conn == nil {
		c, e := s.db.Conn(context.WithoutCancel(ctx))
		if e != nil {
			return nil, e
		}
		s.conn = c
	}
	return s.conn, nil
}

// Must be deferred while coreMu is held. Panic is terminal, not proof that any
// partially-created native object is gone; the independent ownerPin is retained.
func (s *privateSQLSession) contain(err *error) {
	if p := recover(); p != nil {
		*err = privateSQLPanic("private SQL native panic", p)
		s.poison(*err)
	}
}
func (s *privateSQLSession) query(ctx context.Context, q string) (r *privateSQLRows, e error) {
	if q != "SELECT value FROM fixture" {
		return nil, errors.New("SQL outside qualified query slice")
	}
	if e = s.begin(ctx); e != nil {
		return
	}
	defer s.end()
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	defer s.contain(&e)
	if e = s.failure(); e != nil {
		return
	}
	c, e := s.connection(ctx)
	if e != nil {
		return nil, e
	}
	raw, e := c.QueryContext(context.WithoutCancel(ctx), q)
	if e != nil {
		s.poison(e)
		return nil, e
	}
	return s.wrapRows(raw, nil)
}
func (s *privateSQLSession) wrapRows(raw *sql.Rows, parent *privateSQLStmt) (*privateSQLRows, error) {
	ticket, e := s.pin()
	if e != nil {
		ce := raw.Close()
		s.poison(errors.Join(e, ce))
		return nil, errors.Join(e, ce)
	}
	r := &privateSQLRows{s: s, rows: raw, ticket: ticket, parent: parent}
	s.owned(1)
	if parent != nil {
		parent.children++
	}
	return r, nil
}
func (s *privateSQLSession) prepare(ctx context.Context, q string) (p *privateSQLStmt, e error) {
	if q != "SELECT value FROM fixture" {
		return nil, errors.New("SQL outside qualified statement slice")
	}
	if e = s.begin(ctx); e != nil {
		return
	}
	defer s.end()
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	defer s.contain(&e)
	if e = s.failure(); e != nil {
		return
	}
	c, e := s.connection(ctx)
	if e != nil {
		return nil, e
	}
	raw, e := c.PrepareContext(context.WithoutCancel(ctx), q)
	if e != nil {
		s.poison(e)
		return nil, e
	}
	ticket, e := s.pin()
	if e != nil {
		ce := raw.Close()
		s.poison(errors.Join(e, ce))
		return nil, errors.Join(e, ce)
	}
	s.owned(1)
	return &privateSQLStmt{s: s, stmt: raw, ticket: ticket}, nil
}
func (s *privateSQLSession) exec(ctx context.Context, q string) (e error) {
	if q != "INSERT INTO fixture VALUES('must-not-write')" {
		return errors.New("SQL outside qualified readonly failure slice")
	}
	if e = s.begin(ctx); e != nil {
		return
	}
	defer s.end()
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	defer s.contain(&e)
	if e = s.failure(); e != nil {
		return
	}
	c, e := s.connection(ctx)
	if e != nil {
		return e
	}
	ticket, e := s.pin()
	if e != nil {
		return e
	}
	// A panic cannot prove the native execution no longer uses the mapping.
	completed := false
	defer func() {
		if completed {
			if releaseErr := ticket.release(); releaseErr != nil {
				e = errors.Join(e, releaseErr)
				s.poison(releaseErr)
			}
		}
	}()
	_, e = c.ExecContext(context.WithoutCancel(ctx), q)
	if e != nil {
		// Fixed conn.Exec defers stmt.Close; valid finalize destroys the statement
		// even when returning the preceding READONLY step result. This exception
		// applies only to exact base READONLY and independently healthy storage.
		var native *driver.Error
		health := s.healthy()
		if errors.As(e, &native) && native.Code() == sqlite3.SQLITE_READONLY && health == nil {
			completed = true
			return e
		}
		e = errors.Join(e, health)
		s.poison(e)
		return e
	}
	completed = true
	return e
}
func (s *privateSQLSession) close(ctx context.Context) (e error) {
	s.mu.Lock()
	s.closing = true
	s.signalLocked()
	for {
		if s.terminal != nil {
			e = errors.Join(errSHMRetained, s.terminal)
			s.mu.Unlock()
			return
		}
		if s.closed {
			e = s.closeResult
			s.mu.Unlock()
			return
		}
		if err := ctx.Err(); err != nil {
			s.mu.Unlock()
			return err
		}
		if !s.finalizing && s.active == 0 && s.objects == 0 {
			s.finalizing = true
			break
		}
		ch := s.changed
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
		}
		s.mu.Lock()
	}
	s.mu.Unlock()
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	defer func() {
		if p := recover(); p != nil {
			e = privateSQLPanic("SQL final close panic", p)
		}
		s.mu.Lock()
		s.closeResult = e
		if e != nil {
			s.terminal = errors.Join(s.terminal, e)
		} else {
			s.closed = true
		}
		s.signalLocked()
		s.mu.Unlock()
	}()
	// No owned core ticket survives here. Otherwise xShmUnmap would wait on us.
	if s.conn != nil {
		if e = s.conn.Close(); e != nil {
			return
		}
	}
	if e = s.db.Close(); e != nil {
		return
	}
	if e = s.verifyRetired(); e != nil {
		return
	}
	if e = s.ownerPin.release(); e != nil {
		return
	}
	e = s.closeStorage()
	return
}
func (r *privateSQLRows) retire(e error) {
	r.done = true
	r.result = e
	if e != nil {
		r.s.poison(e)
		return
	}
	if e = r.ticket.release(); e != nil {
		r.result = e
		r.s.poison(e)
		return
	}
	if r.parent != nil {
		r.parent.children--
	}
	r.s.owned(-1)
}
func (r *privateSQLRows) panicRetain() {
	if p := recover(); p != nil {
		r.retire(privateSQLPanic("Rows native panic", p))
	}
}
func (r *privateSQLRows) next() (ok bool) {
	r.s.coreMu.Lock()
	defer r.s.coreMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	defer r.panicRetain()
	if r.s.failure() != nil {
		return false
	}
	if r.done {
		return false
	}
	ok = r.rows.Next()
	if !ok {
		r.retire(errors.Join(r.rows.Err(), r.rows.Close()))
	}
	return
}
func (r *privateSQLRows) scan(v ...any) (e error) {
	r.s.coreMu.Lock()
	defer r.s.coreMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if e = r.s.failure(); e != nil {
		return e
	}
	if r.done {
		return errors.Join(errPersistentPermitClosed, r.result)
	}
	return r.rows.Scan(v...)
}
func (r *privateSQLRows) err() error {
	r.s.coreMu.Lock()
	defer r.s.coreMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	return errors.Join(r.result, r.rows.Err(), r.s.failure())
}
func (r *privateSQLRows) close() (e error) {
	r.s.coreMu.Lock()
	defer r.s.coreMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	defer func() {
		if p := recover(); p != nil {
			r.retire(privateSQLPanic("Rows close panic", p))
		}
		e = errors.Join(e, r.result)
	}()
	if fault := r.s.failure(); fault != nil && !r.done {
		return fault
	}
	if !r.done {
		if r.s.beforeRowsClose != nil {
			if fault := r.s.beforeRowsClose(); fault != nil {
				r.retire(fault)
				return fault
			}
		}
		r.retire(r.rows.Close())
	}
	return r.result
}
func (r *privateSQLRows) snapshot() (state sqlite3.ExperimentalPrivateSQLState, e error) {
	r.s.coreMu.Lock()
	defer r.s.coreMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if e = r.s.failure(); e != nil {
		return state, e
	}
	if r.done {
		return state, errPersistentPermitClosed
	}
	defer r.s.contain(&e)
	if r.s.beforeSnapshot != nil {
		r.s.beforeSnapshot()
	}
	return driver.ExperimentalPrivateSQLDiagnostics(r.s.conn)
}
func (p *privateSQLStmt) query(ctx context.Context) (r *privateSQLRows, e error) {
	if e = p.s.begin(ctx); e != nil {
		return
	}
	defer p.s.end()
	p.s.coreMu.Lock()
	defer p.s.coreMu.Unlock()
	defer p.s.contain(&e)
	if e = p.s.failure(); e != nil {
		return
	}
	if p.done {
		return nil, errors.Join(errPersistentPermitClosed, p.result)
	}
	raw, e := p.stmt.QueryContext(context.WithoutCancel(ctx))
	if e != nil {
		p.s.poison(e)
		return nil, e
	}
	return p.s.wrapRows(raw, p)
}
func (p *privateSQLStmt) close() (e error) {
	p.s.coreMu.Lock()
	defer p.s.coreMu.Unlock()
	if e = p.s.failure(); e != nil {
		return e
	}
	if p.done {
		return p.result
	}
	if p.children != 0 {
		return errors.New("statement still owns Rows")
	}
	p.done = true
	defer func() {
		if v := recover(); v != nil {
			e = privateSQLPanic("Stmt close panic", v)
		}
		p.result = e
		if e != nil {
			p.s.poison(e)
		}
	}()
	if p.s.beforeStmtClose != nil {
		if e = p.s.beforeStmtClose(); e != nil {
			return
		}
	}
	if e = p.stmt.Close(); e != nil {
		return
	}
	if e = p.ticket.release(); e != nil {
		return
	}
	p.s.owned(-1)
	return
}

func (s *privateSQLSession) failure() error { s.mu.Lock(); defer s.mu.Unlock(); return s.terminal }

func privateSQLPanic(label string, p any) error {
	if cause, ok := p.(error); ok {
		return fmt.Errorf("%s: %w", label, cause)
	}
	return fmt.Errorf("%s: %v", label, p)
}
