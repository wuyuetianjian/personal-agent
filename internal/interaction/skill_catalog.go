package interaction

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	SkillCatalogLimit   = 256
	SkillRevisionLimit  = 1024
	SkillOperationLimit = 4096
	SkillRetainedBytes  = 64 << 20
)

// SkillIntent accepts only explicit text or catalog/revision IDs. No path, URL,
// eval result, scope override, capability executor, provider or project binding.
type SkillIntent struct {
	RequestID    string `json:"request_id"`
	Kind         string `json:"kind"`
	ManifestText string `json:"manifest_text"`
	CatalogID    string `json:"catalog_id"`
	RevisionID   string `json:"revision_id"`
}
type SkillOperation struct {
	SchemaVersion       int         `json:"schema_version"`
	Namespace           string      `json:"namespace"`
	OperationID         string      `json:"operation_id"`
	SessionID           string      `json:"session_id"`
	ScopeID             string      `json:"scope_id"`
	PolicyDigest        string      `json:"policy_digest"`
	Kind                string      `json:"kind"`
	CatalogID           string      `json:"catalog_id"`
	RevisionID          string      `json:"revision_id"`
	ExpectedGeneration  string      `json:"expected_generation"`
	ExpectedRevision    int64       `json:"expected_revision"`
	OldState            string      `json:"old_state"`
	OldSelectedRevision string      `json:"old_selected_revision"`
	NewState            string      `json:"new_state"`
	NewSelectedRevision string      `json:"new_selected_revision"`
	ParserVersion       string      `json:"parser_version"`
	ReviewVersion       string      `json:"review_version"`
	Review              SkillReview `json:"review"`
	ReviewHash          string      `json:"review_sha256"`
	ExpiresAtMS         int64       `json:"expires_at_ms"`
	Digest              string      `json:"digest"`
	State               string      `json:"state"`
	Decision            string      `json:"decision"`
	DecisionRequestID   string      `json:"decision_request_id"`
	ResultGeneration    string      `json:"result_generation"`
	ResultRevision      int64       `json:"result_revision"`
	Reason              string      `json:"reason"`
}
type SkillCatalogEntry struct {
	ID               string `json:"id"`
	DeclaredID       string `json:"declared_id"`
	Generation       string `json:"generation"`
	Revision         int64  `json:"revision"`
	State            string `json:"state"`
	SelectedRevision string `json:"selected_revision"`
	Evaluation       string `json:"evaluation"`
	Execution        string `json:"execution"`
	ExecutionReason  string `json:"execution_reason"`
}
type SkillProvenance struct {
	SourceKind   string `json:"source_kind"`
	OperationID  string `json:"operation_id"`
	SessionID    string `json:"session_id"`
	ScopeID      string `json:"scope_id"`
	ImportedAtMS int64  `json:"imported_at_ms"`
}
type SkillRevisionRecord struct {
	ID              string          `json:"id"`
	CatalogID       string          `json:"catalog_id"`
	DeclaredVersion string          `json:"declared_version"`
	ManifestText    string          `json:"manifest_text"`
	Review          SkillReview     `json:"review"`
	Provenance      SkillProvenance `json:"provenance"`
}
type SkillVersionEntry struct {
	ID              string `json:"id"`
	CatalogID       string `json:"catalog_id"`
	DeclaredVersion string `json:"declared_version"`
	ArtifactHash    string `json:"artifact_sha256"`
	ContentHash     string `json:"content_sha256"`
	ContentBytes    int    `json:"content_bytes"`
	ParserVersion   string `json:"parser_version"`
	ReviewVersion   string `json:"review_version"`
	Evaluation      string `json:"evaluation"`
	Execution       string `json:"execution"`
	ExecutionReason string `json:"execution_reason"`
}

func skillDigest(p SkillOperation) string { p.Digest = ""; return operatorHash(p) }
func skillCatalogAt(ctx context.Context, tx *sql.Tx, id string) (out SkillCatalogEntry, e error) {
	e = tx.QueryRowContext(ctx, `SELECT id,declared_id,generation,revision,state,selected_revision FROM interaction_skill_catalog WHERE id=?`, id).Scan(&out.ID, &out.DeclaredID, &out.Generation, &out.Revision, &out.State, &out.SelectedRevision)
	if e == nil {
		out.Evaluation, out.Execution, out.ExecutionReason = "not_run", "unavailable", "bridge_not_implemented"
	}
	return
}
func skillRevisionAt(ctx context.Context, tx *sql.Tx, catalog, id string) (out SkillRevisionRecord, e error) {
	var review, provenance, artifact, canonical, content, parser, version string
	e = tx.QueryRowContext(ctx, `SELECT id,catalog_id,declared_version,raw_artifact,artifact_hash,canonical_manifest,content_hash,parser_version,review_version,review,provenance FROM interaction_skill_revisions WHERE id=? AND catalog_id=? AND length(CAST(raw_artifact AS BLOB))<=65536 AND length(CAST(canonical_manifest AS BLOB))<=131072 AND length(CAST(review AS BLOB))<=262144 AND length(CAST(provenance AS BLOB))<=2048`, id, catalog).Scan(&out.ID, &out.CatalogID, &out.DeclaredVersion, &out.ManifestText, &artifact, &canonical, &content, &parser, &version, &review, &provenance)
	if e != nil {
		return
	}
	if e = json.Unmarshal([]byte(review), &out.Review); e != nil {
		return
	}
	if e = json.Unmarshal([]byte(provenance), &out.Provenance); e != nil {
		return
	}
	if artifact != skillBytesHash(out.ManifestText) || artifact != out.Review.ArtifactHash || canonical != out.Review.CanonicalManifest || content != skillBytesHash(canonical) || content != out.Review.ContentHash || parser != out.Review.ParserVersion || version != out.Review.ReviewVersion || out.DeclaredVersion != out.Review.Manifest.Version {
		e = ErrConflict
	}
	return
}
func skillReplay(ctx context.Context, tx *sql.Tx, session, request, fp string) (out SkillOperation, found bool, e error) {
	var owner, old, raw string
	e = tx.QueryRowContext(ctx, `SELECT r.session_id,r.fingerprint,CASE WHEN r.result='' THEN o.result ELSE r.result END FROM interaction_skill_requests r JOIN interaction_skill_operations o ON o.id=r.operation_id WHERE r.request_id=?`, request).Scan(&owner, &old, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return out, false, nil
	}
	if e != nil {
		return
	}
	if owner != session || old != fp {
		return out, true, ErrConflict
	}
	if len(raw) > 512<<10 {
		return out, true, ErrBudget
	}
	e = json.Unmarshal([]byte(raw), &out)
	return out, true, e
}
func skillAudit(ctx context.Context, tx *sql.Tx, p SkillOperation) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO security_audit_log(id,actor,action,target,status,reason,evidence_ids,created_at) VALUES(?,?,?,?,?,?,?,?)`, "skill:"+p.OperationID+":"+p.State, "session:"+p.SessionID, "skill."+p.Kind, p.CatalogID, p.State, p.Reason, "[\""+p.OperationID+"\"]", time.Now().UTC())
	return e
}
func (s *Store) PrepareSkill(ctx context.Context, session string, in SkillIntent) (out SkillOperation, e error) {
	if !operatorRequestID(in.RequestID) || len(in.ManifestText) > MaxSkillManifestBytes {
		return out, ErrInvalid
	}
	switch in.Kind {
	case "import":
		if in.CatalogID != "" || in.RevisionID != "" {
			return out, ErrInvalid
		}
	case "enable":
		if !operatorRequestID(in.CatalogID) || !operatorRequestID(in.RevisionID) || in.ManifestText != "" {
			return out, ErrInvalid
		}
	case "disable":
		if !operatorRequestID(in.CatalogID) || in.RevisionID != "" || in.ManifestText != "" {
			return out, ErrInvalid
		}
	default:
		return out, ErrInvalid
	}
	tx, e := s.queueWrite(ctx, session)
	if e != nil {
		return
	}
	defer tx.Rollback()
	fp := operatorHash([]any{session, "prepare", in})
	if old, found, err := skillReplay(ctx, tx, session, in.RequestID, fp); err != nil || found {
		return old, err
	}
	scope, policy, e := operatorAuthority(ctx, tx, session)
	if e != nil {
		return out, e
	}
	id := in.CatalogID
	review := SkillReview{ParserVersion: SkillParserVersion, ReviewVersion: SkillReviewVersion, Targets: []SkillTargetReview{}, Evaluation: "not_run", Execution: "unavailable", ExecutionReason: "bridge_not_implemented", Notice: skillNotice}
	revision := in.RevisionID
	if in.Kind == "import" {
		review, e = ValidateSkillManifest(in.ManifestText)
		if e != nil {
			return out, e
		}
		id = "skill-" + skillBytesHash(review.Manifest.ID)
	}
	old, e := skillCatalogAt(ctx, tx, id)
	if e != nil && (!errors.Is(e, sql.ErrNoRows) || in.Kind != "import") {
		return out, e
	}
	if in.Kind == "import" {
		var oldID string
		e = tx.QueryRowContext(ctx, `SELECT id FROM interaction_skill_revisions WHERE catalog_id=? AND declared_version=?`, id, review.Manifest.Version).Scan(&oldID)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		if oldID != "" {
			r, err := skillRevisionAt(ctx, tx, id, oldID)
			if err != nil {
				return out, err
			}
			if r.ManifestText != in.ManifestText || operatorHash(r.Review) != operatorHash(review) {
				return out, ErrConflict
			}
			revision = oldID
		} else {
			revision = "skill-rev-" + uuid.NewString()
		}
	} else {
		if in.Kind == "disable" {
			revision = old.SelectedRevision
		}
		if revision != "" {
			r, err := skillRevisionAt(ctx, tx, id, revision)
			if err != nil {
				return out, err
			}
			review = r.Review
		}
	}
	newState, newSelected := old.State, old.SelectedRevision
	if old.ID == "" {
		newState = "disabled"
	}
	if in.Kind == "enable" {
		newState, newSelected = "enabled", revision
	}
	if in.Kind == "disable" {
		newState, newSelected = "disabled", ""
	}
	out = SkillOperation{SchemaVersion: 1, Namespace: "runtime-rebuild-v1", OperationID: in.RequestID, SessionID: session, ScopeID: scope, PolicyDigest: policy, Kind: in.Kind, CatalogID: id, RevisionID: revision, ExpectedGeneration: old.Generation, ExpectedRevision: old.Revision, OldState: old.State, OldSelectedRevision: old.SelectedRevision, NewState: newState, NewSelectedRevision: newSelected, ParserVersion: SkillParserVersion, ReviewVersion: SkillReviewVersion, Review: review, ReviewHash: operatorHash(review), ExpiresAtMS: time.Now().Add(10 * time.Minute).UnixMilli(), State: "pending", Reason: skillNotice}
	out.Digest = skillDigest(out)
	raw, _ := json.Marshal(out)
	// Reserve space for original preview, current result, decision result, staging,
	// fingerprints, provenance and bounded audit/history labels. Never auto-purge.
	reserve := 4*(len(raw)+4096) + len(in.ManifestText)
	var n, total, pending int
	if e = tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(reserved_bytes),0),coalesce(sum(CASE WHEN state='pending' AND json_extract(preview,'$.expires_at_ms')>? THEN 1 ELSE 0 END),0) FROM interaction_skill_operations`, time.Now().UnixMilli()).Scan(&n, &total, &pending); e != nil {
		return out, e
	}
	if n >= SkillOperationLimit || total+reserve > SkillRetainedBytes || pending >= 128 {
		return out, ErrBudget
	}
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM interaction_skill_operations WHERE session_id=? AND state='pending' AND json_extract(preview,'$.expires_at_ms')>?`, session, time.Now().UnixMilli()).Scan(&n); e != nil {
		return out, e
	}
	if n >= 64 {
		return out, ErrBudget
	}
	if in.Kind == "import" {
		if e = skillImportCapacity(ctx, tx, old.ID == "", revision, review, in.ManifestText); e != nil {
			return out, e
		}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO interaction_skill_operations VALUES(?,?,?,?,?,'pending',?)`, out.OperationID, session, string(raw), in.ManifestText, reserve, string(raw)); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO interaction_skill_requests VALUES(?,?,?,?,'')`, in.RequestID, session, out.OperationID, fp); e != nil {
		return out, e
	}
	if e = skillAudit(ctx, tx, out); e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func skillImportCapacity(ctx context.Context, tx *sql.Tx, newCatalog bool, revision string, review SkillReview, raw string) error {
	var n, total, existing int
	if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM interaction_skill_catalog`).Scan(&n); e != nil {
		return e
	}
	if newCatalog && n >= SkillCatalogLimit {
		return ErrBudget
	}
	if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM interaction_skill_revisions WHERE id=?`, revision).Scan(&existing); e != nil {
		return e
	}
	if existing != 0 {
		return nil
	}
	if e := tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(length(CAST(raw_artifact AS BLOB))+length(CAST(canonical_manifest AS BLOB))+length(CAST(review AS BLOB))+length(CAST(provenance AS BLOB))),0) FROM interaction_skill_revisions`).Scan(&n, &total); e != nil {
		return e
	}
	b, _ := json.Marshal(review)
	if n >= SkillRevisionLimit || total+len(raw)+len(review.CanonicalManifest)+len(b)+2048 > SkillRetainedBytes {
		return ErrBudget
	}
	return nil
}
func (s *Store) DecideSkill(ctx context.Context, session string, in OperatorDecision) (SkillOperation, error) {
	return s.decideSkill(ctx, session, in, time.Now())
}
func (s *Store) decideSkill(ctx context.Context, session string, in OperatorDecision, now time.Time) (out SkillOperation, e error) {
	if !operatorRequestID(in.RequestID) || !operatorRequestID(in.OperationID) || len(in.Digest) != 64 || !skillEnum(in.Decision, "confirm", "cancel") {
		return out, ErrInvalid
	}
	tx, e := s.queueWrite(ctx, session)
	if e != nil {
		return
	}
	defer tx.Rollback()
	fp := operatorHash([]any{session, "decision", in})
	if old, found, err := skillReplay(ctx, tx, session, in.RequestID, fp); err != nil || found {
		return old, err
	}
	var raw, state, staged string
	e = tx.QueryRowContext(ctx, `SELECT preview,state,staged FROM interaction_skill_operations WHERE id=? AND session_id=?`, in.OperationID, session).Scan(&raw, &state, &staged)
	if errors.Is(e, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if e != nil {
		return
	}
	if e = json.Unmarshal([]byte(raw), &out); e != nil {
		return
	}
	if state != "pending" || out.Digest != in.Digest || skillDigest(out) != in.Digest {
		return out, ErrConflict
	}
	out.Decision, out.DecisionRequestID = in.Decision, in.RequestID
	reject := func(reason string) { out.State = "rejected"; out.Reason = reason }
	scope, policy, authorityErr := operatorAuthority(ctx, tx, session)
	switch {
	case now.UnixMilli() >= out.ExpiresAtMS:
		out.State, out.Reason = "expired", "Preview expired; no catalog change."
	case in.Decision == "cancel":
		reject("User cancelled; no catalog change.")
	case authorityErr != nil || scope != out.ScopeID || policy != out.PolicyDigest:
		reject("Original metadata authority changed or unavailable; no catalog change.")
	case out.ParserVersion != SkillParserVersion || out.ReviewVersion != SkillReviewVersion || out.ReviewHash != operatorHash(out.Review):
		reject("Parser or review version changed; prepare a new preview.")
	default:
		old, err := skillCatalogAt(ctx, tx, out.CatalogID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if old.Generation != out.ExpectedGeneration || old.Revision != out.ExpectedRevision || old.State != out.OldState || old.SelectedRevision != out.OldSelectedRevision {
			reject("Catalog generation, revision or selection changed; prepare a new preview.")
			break
		}
		artifact := staged
		if out.Kind != "import" && out.RevisionID != "" {
			r, err := skillRevisionAt(ctx, tx, out.CatalogID, out.RevisionID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrConflict) {
					reject("Pinned immutable revision unavailable or changed.")
					break
				}
				return out, err
			}
			artifact = r.ManifestText
			if operatorHash(r.Review) != out.ReviewHash {
				reject("Pinned revision review changed.")
				break
			}
		}
		if out.RevisionID != "" {
			review, err := ValidateSkillManifest(artifact)
			if err != nil || operatorHash(review) != out.ReviewHash {
				reject("Exact artifact or review no longer matches; prepare a new preview.")
				break
			}
		}
		if e = applySkill(ctx, tx, &out, old, artifact, now); e != nil {
			return out, e
		}
	}
	result, _ := json.Marshal(out)
	res, e := tx.ExecContext(ctx, `UPDATE interaction_skill_operations SET state=?,result=? WHERE id=? AND state='pending'`, out.State, string(result), out.OperationID)
	if e = changed(res, e); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO interaction_skill_requests VALUES(?,?,?,?,?)`, in.RequestID, session, out.OperationID, fp, string(result)); e != nil {
		return out, e
	}
	if e = skillAudit(ctx, tx, out); e != nil {
		return out, e
	}
	if e = tx.Commit(); e != nil {
		return out, e
	}
	if out.State == "committed" && (out.Kind == "enable" || out.Kind == "disable") {
		s.signalRevokedSkillRuns(ctx, out.CatalogID)
	}
	return out, nil
}
func applySkill(ctx context.Context, tx *sql.Tx, p *SkillOperation, old SkillCatalogEntry, artifact string, now time.Time) error {
	noop := false
	if p.Kind == "import" {
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT id FROM interaction_skill_revisions WHERE catalog_id=? AND declared_version=?`, p.CatalogID, p.Review.Manifest.Version).Scan(&existing)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if existing != "" {
			r, err := skillRevisionAt(ctx, tx, p.CatalogID, existing)
			if err != nil {
				return err
			}
			if r.ManifestText != artifact || existing != p.RevisionID {
				p.State, p.Reason = "rejected", "Declared version already has different immutable bytes; no catalog change."
				return nil
			}
			noop = true
		}
	} else {
		noop = old.State == p.NewState && old.SelectedRevision == p.NewSelectedRevision
	}
	p.ResultGeneration, p.ResultRevision = old.Generation, old.Revision
	if noop {
		p.State, p.Reason = "no_op", "Already at exact immutable artifact or catalog selection; no new catalog effect."
		return nil
	}
	if p.Kind == "import" {
		if e := skillImportCapacity(ctx, tx, old.ID == "", p.RevisionID, p.Review, artifact); e != nil {
			return e
		}
	}
	if old.ID == "" {
		p.ResultGeneration, p.ResultRevision = uuid.NewString(), 1
		if _, e := tx.ExecContext(ctx, `INSERT INTO interaction_skill_catalog VALUES(?,?,?,1,'disabled','')`, p.CatalogID, p.Review.Manifest.ID, p.ResultGeneration); e != nil {
			return e
		}
	}
	if p.Kind == "import" {
		review, _ := json.Marshal(p.Review)
		provenance, _ := json.Marshal(SkillProvenance{SourceKind: "text", OperationID: p.OperationID, SessionID: p.SessionID, ScopeID: p.ScopeID, ImportedAtMS: now.UnixMilli()})
		if _, e := tx.ExecContext(ctx, `INSERT INTO interaction_skill_revisions VALUES(?,?,?,?,?,?,?,?,?,?,?)`, p.RevisionID, p.CatalogID, p.Review.Manifest.Version, artifact, p.Review.ArtifactHash, p.Review.CanonicalManifest, p.Review.ContentHash, p.ParserVersion, p.ReviewVersion, string(review), string(provenance)); e != nil {
			return e
		}
	}
	if old.ID != "" {
		p.ResultRevision = old.Revision + 1
		res, e := tx.ExecContext(ctx, `UPDATE interaction_skill_catalog SET revision=revision+1,state=?,selected_revision=? WHERE id=? AND generation=? AND revision=? AND state=? AND selected_revision=?`, p.NewState, p.NewSelectedRevision, p.CatalogID, old.Generation, old.Revision, old.State, old.SelectedRevision)
		if e = changed(res, e); e != nil {
			return e
		}
	}
	if _, e := tx.ExecContext(ctx, `INSERT INTO interaction_skill_history VALUES(?,?,?,?,?,?)`, p.OperationID, p.CatalogID, p.ResultGeneration, p.ResultRevision, p.NewState, p.NewSelectedRevision); e != nil {
		return e
	}
	p.State, p.Reason = "committed", skillNotice
	return nil
}

// Durable revocation is already committed. Signals only reduce latency and may
// be lost without widening dispatch or finalization authority.
func (s *Store) signalRevokedSkillRuns(ctx context.Context, catalog string) {
	rows, err := s.db.QueryContext(ctx, `SELECT run_id FROM interaction_skill_runs WHERE catalog_id=? AND revoked=1 AND terminal=0`, catalog)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	activeOwners.Lock()
	defer activeOwners.Unlock()
	for _, id := range ids {
		if cancel := activeOwners.cancels[id]; cancel != nil {
			cancel()
		}
	}
}
