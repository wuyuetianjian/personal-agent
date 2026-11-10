-- Execution authority is separate from catalog lifecycle. Existing trusted
-- policy rows retain their exact behavior; this migration rewrites no policy.
CREATE TABLE project_execution_classification (
 project_id TEXT PRIMARY KEY,
 execution_class TEXT NOT NULL CHECK(execution_class IN ('legacy_policy','metadata_only'))
);
INSERT INTO project_execution_classification SELECT id,'legacy_policy' FROM projects;
-- A future metadata-create transaction must pre-seed metadata_only before its
-- INSERT-only project write. Ordinary legacy INSERT fixtures remain compatible.
-- Retain tombstones forever: delete/recreate must never promote metadata IDs.
CREATE TRIGGER project_execution_insert AFTER INSERT ON projects BEGIN
 INSERT INTO project_execution_classification(project_id,execution_class)
 SELECT NEW.id,'legacy_policy' WHERE NOT EXISTS (SELECT 1 FROM project_execution_classification WHERE project_id=NEW.id);
END;
-- REPLACE does not fire delete triggers with SQLite's default recursive
-- triggers setting. Guard insertion too, before conflict replacement occurs.
CREATE TRIGGER project_execution_immutable_insert BEFORE INSERT ON project_execution_classification
WHEN EXISTS (SELECT 1 FROM project_execution_classification WHERE project_id=NEW.project_id) BEGIN
 SELECT RAISE(ABORT,'project execution classification already exists');
END;
CREATE TRIGGER project_execution_immutable_update BEFORE UPDATE ON project_execution_classification BEGIN
 SELECT RAISE(ABORT,'project execution classification is immutable');
END;
CREATE TRIGGER project_execution_immutable_delete BEFORE DELETE ON project_execution_classification BEGIN
 SELECT RAISE(ABORT,'project execution classification is retained');
END;
-- Never allow legacy whole-object upsert to modify metadata-only authority.
-- Catalog-only name edits intentionally remain possible.
CREATE TRIGGER project_execution_policy_guard BEFORE UPDATE OF privacy_class,repository_refs_json,knowledge_scopes_json,memory_scope,allowed_skills_json,allowed_capabilities_json,allowed_coding_agents_json,allowed_models_json,budget_policy ON projects
WHEN COALESCE((SELECT execution_class FROM project_execution_classification WHERE project_id=OLD.id),'') <> 'legacy_policy' BEGIN
 SELECT RAISE(ABORT,'project policy write denied');
END;
CREATE TRIGGER project_execution_identity_guard BEFORE UPDATE OF id ON projects
WHEN CAST(OLD.id AS BLOB) != CAST(NEW.id AS BLOB) BEGIN
 SELECT RAISE(ABORT,'project identity is immutable');
END;
