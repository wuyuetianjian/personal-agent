package interaction

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Catalog is service-local metadata visible to any bound API session, not a
// workspace grant. Operation/recovery evidence remains original-session only.
func (s *Store) skillRead(ctx context.Context, session string) (*sql.Tx, error) {
	tx, e := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	if _, _, e = operatorAuthority(ctx, tx, session); e != nil {
		tx.Rollback()
		return nil, e
	}
	return tx, nil
}
func (s *Store) SkillOperation(ctx context.Context, session, request string) (out SkillOperation, e error) {
	if !operatorRequestID(request) {
		return out, ErrInvalid
	}
	var raw string
	e = s.db.QueryRowContext(ctx, `SELECT CASE WHEN r.result='' THEN o.result ELSE r.result END FROM interaction_skill_requests r JOIN interaction_skill_operations o ON o.id=r.operation_id WHERE r.request_id=? AND r.session_id=? AND length(CAST(o.result AS BLOB))<=524288 AND length(CAST(r.result AS BLOB))<=524288`, request, session).Scan(&raw)
	if e != nil {
		return
	}
	e = json.Unmarshal([]byte(raw), &out)
	if e == nil && out.State == "pending" && time.Now().UnixMilli() >= out.ExpiresAtMS {
		out.State, out.Reason = "expired", "Preview expired; no catalog change."
	}
	return
}
func (s *Store) SkillCatalogItem(ctx context.Context, session, id string) (SkillCatalogEntry, error) {
	if !operatorRequestID(id) {
		return SkillCatalogEntry{}, ErrInvalid
	}
	tx, e := s.skillRead(ctx, session)
	if e != nil {
		return SkillCatalogEntry{}, e
	}
	defer tx.Rollback()
	return skillCatalogAt(ctx, tx, id)
}
func (s *Store) SkillRevision(ctx context.Context, session, catalog, revision string) (SkillRevisionRecord, error) {
	if !operatorRequestID(catalog) || !operatorRequestID(revision) {
		return SkillRevisionRecord{}, ErrInvalid
	}
	tx, e := s.skillRead(ctx, session)
	if e != nil {
		return SkillRevisionRecord{}, e
	}
	defer tx.Rollback()
	return skillRevisionAt(ctx, tx, catalog, revision)
}
func skillPage(after string, limit int) bool {
	return limit >= 1 && limit <= 50 && (after == "" || operatorRequestID(after))
}
func (s *Store) ListSkills(ctx context.Context, session, after string, limit int) (out []SkillCatalogEntry, e error) {
	if !skillPage(after, limit) {
		return nil, ErrInvalid
	}
	tx, e := s.skillRead(ctx, session)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, `SELECT id,declared_id,generation,revision,state,selected_revision FROM interaction_skill_catalog WHERE id>? ORDER BY id LIMIT ?`, after, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out = []SkillCatalogEntry{}
	for rows.Next() {
		var item SkillCatalogEntry
		if e = rows.Scan(&item.ID, &item.DeclaredID, &item.Generation, &item.Revision, &item.State, &item.SelectedRevision); e != nil {
			return nil, e
		}
		item.Evaluation, item.Execution, item.ExecutionReason = "not_run", "unavailable", "bridge_not_implemented"
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *Store) SkillVersions(ctx context.Context, session, catalog, after string, limit int) (out []SkillVersionEntry, e error) {
	if !operatorRequestID(catalog) || !skillPage(after, limit) {
		return nil, ErrInvalid
	}
	tx, e := s.skillRead(ctx, session)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if _, e = skillCatalogAt(ctx, tx, catalog); e != nil {
		return nil, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT id,catalog_id,declared_version,artifact_hash,content_hash,length(CAST(raw_artifact AS BLOB)),parser_version,review_version FROM interaction_skill_revisions WHERE catalog_id=? AND id>? ORDER BY id LIMIT ?`, catalog, after, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out = []SkillVersionEntry{}
	for rows.Next() {
		var item SkillVersionEntry
		if e = rows.Scan(&item.ID, &item.CatalogID, &item.DeclaredVersion, &item.ArtifactHash, &item.ContentHash, &item.ContentBytes, &item.ParserVersion, &item.ReviewVersion); e != nil {
			return nil, e
		}
		item.Evaluation, item.Execution, item.ExecutionReason = "not_run", "unavailable", "bridge_not_implemented"
		out = append(out, item)
	}
	return out, rows.Err()
}
