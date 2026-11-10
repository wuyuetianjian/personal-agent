package api

import (
	"agent/internal/interaction"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type SkillCatalogPage struct {
	SchemaVersion int                             `json:"schema_version"`
	Namespace     string                          `json:"namespace"`
	SessionID     string                          `json:"session_id"`
	Items         []interaction.SkillCatalogEntry `json:"items"`
	NextCursor    string                          `json:"next_cursor"`
}
type SkillVersionPage struct {
	SchemaVersion int                             `json:"schema_version"`
	Namespace     string                          `json:"namespace"`
	SessionID     string                          `json:"session_id"`
	CatalogID     string                          `json:"catalog_id"`
	Items         []interaction.SkillVersionEntry `json:"items"`
	NextCursor    string                          `json:"next_cursor"`
}

var skillWireID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

func skillWireIdentity(v string) bool { return skillWireID.MatchString(v) && v != "." && v != ".." }
func skillRoute(p []string) bool      { return len(p) >= 3 && p[0] == "sessions" && p[2] == "skills" }
func skillCursor(session, catalog, last string) string {
	raw, _ := json.Marshal([]string{session, catalog, last})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func (s *InteractionService) skills(w http.ResponseWriter, r *http.Request, p []string) bool {
	if !skillRoute(p) {
		return false
	}
	// The public catalog has one spelling. Escaped separators/identities and
	// trailing slashes never alias a mutating or recovery route.
	if r.URL.EscapedPath() != r.URL.Path || r.URL.Path != "/api/v1/"+strings.Join(p, "/") {
		wireError(w, 400, "invalid_request")
		return true
	}
	for _, part := range p {
		if !skillWireIdentity(part) {
			wireError(w, 400, "invalid_request")
			return true
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if e := s.operatorStoredSession(ctx, p[1]); e != nil {
		wireError(w, 404, "session_not_found")
		return true
	}
	var out any
	var e error
	isPage := r.Method == "GET" && (len(p) == 3 || len(p) == 5 && p[4] == "versions")
	if isPage {
		q := r.URL.Query()
		if !knowledgeQuery(q, "limit", "cursor") {
			wireError(w, 400, "invalid_query")
			return true
		}
		limit := 20
		if q.Has("limit") {
			limit, e = strconv.Atoi(q.Get("limit"))
			if e != nil || strconv.Itoa(limit) != q.Get("limit") || limit < 1 || limit > 50 {
				wireError(w, 400, "invalid_query")
				return true
			}
		}
		catalog := ""
		if len(p) == 5 {
			catalog = p[3]
		}
		after := ""
		if q.Has("cursor") {
			cursor := q.Get("cursor")
			if len(cursor) > 1024 {
				wireError(w, 400, "invalid_cursor")
				return true
			}
			raw, err := base64.RawURLEncoding.DecodeString(cursor)
			var ids []string
			if err != nil || json.Unmarshal(raw, &ids) != nil || len(ids) != 3 || ids[0] != p[1] || ids[1] != catalog || !skillWireIdentity(ids[2]) || skillCursor(p[1], catalog, ids[2]) != cursor {
				wireError(w, 400, "invalid_cursor")
				return true
			}
			after = ids[2]
		}
		next := ""
		if catalog == "" {
			var items []interaction.SkillCatalogEntry
			items, e = s.store.ListSkills(ctx, p[1], after, limit)
			if len(items) == limit {
				next = skillCursor(p[1], catalog, items[len(items)-1].ID)
			}
			out = SkillCatalogPage{1, "runtime-rebuild-v1", p[1], items, next}
		} else {
			var items []interaction.SkillVersionEntry
			items, e = s.store.SkillVersions(ctx, p[1], catalog, after, limit)
			if len(items) == limit {
				next = skillCursor(p[1], catalog, items[len(items)-1].ID)
			}
			out = SkillVersionPage{1, "runtime-rebuild-v1", p[1], catalog, items, next}
		}
	} else if r.URL.RawQuery != "" {
		wireError(w, 400, "invalid_query")
		return true
	} else if r.Method == "GET" && len(p) == 5 && p[3] == "operations" {
		out, e = s.store.SkillOperation(ctx, p[1], p[4])
	} else if r.Method == "GET" && len(p) == 4 {
		out, e = s.store.SkillCatalogItem(ctx, p[1], p[3])
	} else if r.Method == "GET" && len(p) == 6 && p[4] == "versions" {
		out, e = s.store.SkillRevision(ctx, p[1], p[3], p[5])
	} else if r.Method == "POST" && len(p) == 4 && (p[3] == "validate" || p[3] == "preview" || p[3] == "decision") {
		// One UTF-8 byte needs at most six JSON bytes (ASCII \u00XX). Fixed
		// envelope allowance is bounded too; the decoded parser rechecks 64 KiB.
		const cap = 6*interaction.MaxSkillManifestBytes + 4096
		raw, err := io.ReadAll(io.LimitReader(r.Body, cap+1))
		if err != nil || len(raw) > cap {
			wireError(w, 400, "invalid_request")
			return true
		}
		fields := []string{"request_id", "kind", "manifest_text", "catalog_id", "revision_id"}
		if p[3] == "validate" {
			fields = []string{"manifest_text"}
		}
		if p[3] == "decision" {
			fields = []string{"request_id", "operation_id", "digest", "decision"}
		}
		v, ok := operatorBody(raw, fields)
		if !ok {
			wireError(w, 400, "invalid_request")
			return true
		}
		switch p[3] {
		case "validate":
			out, e = interaction.ValidateSkillManifest(v["manifest_text"])
		case "preview":
			out, e = s.store.PrepareSkill(ctx, p[1], interaction.SkillIntent{RequestID: v["request_id"], Kind: v["kind"], ManifestText: v["manifest_text"], CatalogID: v["catalog_id"], RevisionID: v["revision_id"]})
		case "decision":
			out, e = s.store.DecideSkill(ctx, p[1], interaction.OperatorDecision{RequestID: v["request_id"], OperationID: v["operation_id"], Digest: v["digest"], Decision: v["decision"]})
		}
	} else {
		wireError(w, 404, "not_found")
		return true
	}
	switch {
	case errors.Is(e, interaction.ErrInvalid), errors.Is(e, interaction.ErrBudget):
		wireError(w, 400, "skill_invalid_request")
	case errors.Is(e, interaction.ErrConflict):
		wireError(w, 409, "skill_request_conflict")
	case errors.Is(e, sql.ErrNoRows):
		wireError(w, 404, "skill_not_found")
	case e != nil:
		wireError(w, 500, "skill_outcome_unknown")
	default:
		wireJSON(w, 200, out)
	}
	return true
}
