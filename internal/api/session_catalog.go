package api

import (
	"agent/internal/interaction"
	"context"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type activeProject struct {
	interaction.ProjectLabel
	ExecutionClass string `json:"execution_class"`
}
type activeProjectPage struct {
	SchemaVersion int             `json:"schema_version"`
	Items         []activeProject `json:"items"`
	NextCursor    string          `json:"next_cursor"`
}

// Pre-session discovery is metadata-only: no session, policy, resolver or process.
// Cursor is scoped to this fixed active projection, not another inventory route.
func (s *InteractionService) readActiveProjects(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 25
	var after int64
	for k, v := range q {
		if (k != "limit" && k != "cursor") || len(v) != 1 || v[0] == "" || len(v[0]) > 128 {
			wireError(w, 400, "invalid_query")
			return
		}
	}
	if q.Has("limit") {
		n, e := strconv.Atoi(q.Get("limit"))
		if e != nil || n < 1 || n > 50 {
			wireError(w, 400, "invalid_query")
			return
		}
		limit = n
	}
	if q.Has("cursor") {
		raw, e := base64.RawURLEncoding.DecodeString(q.Get("cursor"))
		const prefix = "active-projects-v1:"
		if e != nil || !strings.HasPrefix(string(raw), prefix) {
			wireError(w, 400, "invalid_cursor")
			return
		}
		a := strings.TrimPrefix(string(raw), prefix)
		after, e = strconv.ParseInt(a, 10, 64)
		if e != nil || after < 1 || strconv.FormatInt(after, 10) != a {
			wireError(w, 400, "invalid_cursor")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	rows, e := s.runtime.DB.SQL.QueryContext(ctx, `SELECT p.rowid,p.id,p.name,l.generation,l.revision,c.execution_class FROM projects p JOIN project_catalog_lifecycle l ON l.project_id=p.id JOIN project_execution_classification c ON c.project_id=p.id WHERE l.state='active' AND p.rowid>? ORDER BY p.rowid LIMIT ?`, after, limit+1)
	if e != nil {
		wireError(w, 500, "storage_error")
		return
	}
	defer rows.Close()
	page := activeProjectPage{SchemaVersion: 1, Items: []activeProject{}}
	var last int64
	for rows.Next() {
		var row int64
		var p activeProject
		if e = rows.Scan(&row, &p.ProjectID, &p.Name, &p.Generation, &p.Revision, &p.ExecutionClass); e != nil || !p.Valid() || (p.ExecutionClass != "metadata_only" && p.ExecutionClass != "legacy_policy") {
			wireError(w, 500, "storage_error")
			return
		}
		if len(page.Items) == limit {
			page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte("active-projects-v1:" + strconv.FormatInt(last, 10)))
			break
		}
		page.Items = append(page.Items, p)
		last = row
	}
	if rows.Err() != nil {
		wireError(w, 500, "storage_error")
		return
	}
	wireJSON(w, 200, page)
}
