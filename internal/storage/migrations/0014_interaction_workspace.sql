CREATE TABLE interaction_workspaces (
 session_id TEXT PRIMARY KEY REFERENCES interaction_sessions(id),
 scope_id TEXT NOT NULL, policy TEXT NOT NULL, root_identity TEXT NOT NULL
);
CREATE TRIGGER interaction_workspace_immutable BEFORE UPDATE ON interaction_workspaces
 BEGIN SELECT RAISE(ABORT,'immutable workspace'); END;
CREATE TRIGGER interaction_workspace_no_delete BEFORE DELETE ON interaction_workspaces
 BEGIN SELECT RAISE(ABORT,'immutable workspace'); END;
CREATE TABLE interaction_tool_proposals (
 id TEXT PRIMARY KEY, run_id TEXT NOT NULL REFERENCES interaction_runs(id),
 call_id TEXT NOT NULL, prepared TEXT NOT NULL, executed INTEGER NOT NULL DEFAULT 0,
 UNIQUE(run_id,call_id)
);
CREATE TRIGGER interaction_tool_proposal_immutable BEFORE UPDATE OF id,run_id,call_id,prepared ON interaction_tool_proposals
 BEGIN SELECT RAISE(ABORT,'immutable prepared tool'); END;
CREATE TABLE interaction_artifacts (
 session_id TEXT NOT NULL REFERENCES interaction_sessions(id),
 id TEXT NOT NULL, content BLOB NOT NULL, PRIMARY KEY(session_id,id)
);
-- Trusted ingestion assigns provenance separately from document/model metadata.
CREATE TABLE project_document_scopes (
 document_id TEXT PRIMARY KEY REFERENCES documents(id) ON DELETE CASCADE, project_id TEXT NOT NULL, content_hash TEXT NOT NULL
);
CREATE TRIGGER project_document_scope_immutable BEFORE UPDATE ON project_document_scopes
 BEGIN SELECT RAISE(ABORT,'immutable document scope'); END;
-- Source-provided content_hash is not version authority. Any document/chunk
-- mutation revokes trusted ingestion ownership; re-assignment is explicit.
CREATE TRIGGER project_document_scope_revoke_update AFTER UPDATE ON documents
 BEGIN DELETE FROM project_document_scopes WHERE document_id=NEW.id; END;
CREATE TRIGGER project_document_scope_revoke_chunk_update AFTER UPDATE ON document_chunks
 BEGIN DELETE FROM project_document_scopes WHERE document_id=OLD.document_id OR document_id=NEW.document_id; END;
CREATE TRIGGER project_document_scope_revoke_chunk_insert AFTER INSERT ON document_chunks
 BEGIN DELETE FROM project_document_scopes WHERE document_id=NEW.document_id; END;
CREATE TRIGGER project_document_scope_revoke_chunk_delete AFTER DELETE ON document_chunks
 BEGIN DELETE FROM project_document_scopes WHERE document_id=OLD.document_id; END;
