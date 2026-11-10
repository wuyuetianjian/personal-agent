-- Descriptive provenance only. No FK to the live catalog: historical labels
-- survive rename, archive, delete and recreation, without granting authority.
CREATE TABLE interaction_session_project_labels (
 session_id TEXT PRIMARY KEY REFERENCES interaction_sessions(id),
 project_id TEXT NOT NULL,
 generation TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision > 0 AND revision <= 9007199254740991),
 name TEXT NOT NULL
);
CREATE TRIGGER interaction_project_label_no_replace BEFORE INSERT ON interaction_session_project_labels
WHEN EXISTS(SELECT 1 FROM interaction_session_project_labels WHERE session_id=NEW.session_id)
BEGIN SELECT RAISE(ABORT,'immutable project label'); END;
CREATE TRIGGER interaction_project_label_no_update BEFORE UPDATE ON interaction_session_project_labels
BEGIN SELECT RAISE(ABORT,'immutable project label'); END;
CREATE TRIGGER interaction_project_label_no_delete BEFORE DELETE ON interaction_session_project_labels
BEGIN SELECT RAISE(ABORT,'immutable project label'); END;
-- Historical name retained for compatibility. All new session preparations now
-- reserve this single-use ledger, including preparations without MCP effects.
ALTER TABLE interaction_mcp_admissions ADD COLUMN kind TEXT NOT NULL DEFAULT 'mcp' CHECK(kind IN ('mcp','session'));
