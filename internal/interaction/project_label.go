package interaction

import (
	"context"
	"database/sql"
	"encoding/hex"
	"strings"
	"unicode/utf8"
)

// ProjectLabel is an immutable descriptive snapshot. It must never become
// workspace authority, a retrieval scope, or legacy RunRequest.ProjectID.
type ProjectLabel struct {
	ProjectID  string `json:"project_id"`
	Generation string `json:"generation"`
	Revision   int64  `json:"revision"`
	Name       string `json:"name"`
}

func (p ProjectLabel) Valid() bool {
	generation, e := hex.DecodeString(p.Generation)
	return p.ProjectID != "" && len(p.ProjectID) <= 512 && utf8.ValidString(p.ProjectID) && !strings.ContainsRune(p.ProjectID, 0) && e == nil && len(generation) == 16 && p.Generation == strings.ToLower(p.Generation) && p.Revision > 0 && p.Revision <= 9007199254740991 && len(p.Name) <= 2048 && utf8.ValidString(p.Name)
}

type labelQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// ActiveProjectLabel must be called within the publishing write transaction.
// A preflight call is only an early refusal, never authority to publish later.
func ActiveProjectLabel(ctx context.Context, q labelQuery, id string) (*ProjectLabel, error) {
	if id == "" {
		return nil, nil
	}
	p := &ProjectLabel{ProjectID: id}
	var state, class string
	e := q.QueryRowContext(ctx, `SELECT p.name,l.generation,l.revision,l.state,c.execution_class FROM projects p JOIN project_catalog_lifecycle l ON l.project_id=p.id JOIN project_execution_classification c ON c.project_id=p.id WHERE p.id=?`, id).Scan(&p.Name, &p.Generation, &p.Revision, &state, &class)
	if e != nil {
		return nil, e
	}
	if !p.Valid() || state != "active" || (class != "metadata_only" && class != "legacy_policy") {
		return nil, ErrInvalid
	}
	return p, nil
}
func projectLabelFrom(ctx context.Context, q labelQuery, id string) (*ProjectLabel, error) {
	p := &ProjectLabel{}
	e := q.QueryRowContext(ctx, `SELECT project_id,generation,revision,name FROM interaction_session_project_labels WHERE session_id=?`, id).Scan(&p.ProjectID, &p.Generation, &p.Revision, &p.Name)
	if e == sql.ErrNoRows {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if !p.Valid() {
		return nil, ErrInvalid
	}
	return p, nil
}
func (s *Store) ProjectLabel(ctx context.Context, id string) (*ProjectLabel, error) {
	return projectLabelFrom(ctx, s.db, id)
}
func InsertProjectLabel(ctx context.Context, tx *sql.Tx, id string, p *ProjectLabel) error {
	if p == nil {
		return nil
	}
	if !p.Valid() {
		return ErrInvalid
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO interaction_session_project_labels(session_id,project_id,generation,revision,name) VALUES(?,?,?,?,?)`, id, p.ProjectID, p.Generation, p.Revision, p.Name)
	return e
}
func copyProjectLabel(ctx context.Context, tx *sql.Tx, source, target string) error {
	p, e := projectLabelFrom(ctx, tx, source)
	if e != nil {
		return e
	}
	return InsertProjectLabel(ctx, tx, target, p)
}
