-- Context snapshots contain evidence only, never executable runs or approvals.
CREATE TABLE interaction_session_branches (
 session_id TEXT PRIMARY KEY REFERENCES interaction_sessions(id),
 parent_session_id TEXT NOT NULL REFERENCES interaction_sessions(id),
 boundary_run_id TEXT NOT NULL, source_cursor INTEGER NOT NULL,
 kind TEXT NOT NULL, transcript TEXT NOT NULL
);
CREATE TRIGGER interaction_branch_immutable BEFORE UPDATE ON interaction_session_branches BEGIN SELECT RAISE(ABORT,'immutable branch'); END;
CREATE TRIGGER interaction_branch_no_delete BEFORE DELETE ON interaction_session_branches BEGIN SELECT RAISE(ABORT,'immutable branch'); END;
CREATE TABLE interaction_context_versions (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES interaction_sessions(id),
 source_cursor INTEGER NOT NULL, original TEXT NOT NULL, compacted TEXT NOT NULL,
 method TEXT NOT NULL, omitted_messages INTEGER NOT NULL
);
CREATE TRIGGER interaction_context_immutable BEFORE UPDATE ON interaction_context_versions BEGIN SELECT RAISE(ABORT,'immutable context version'); END;
CREATE TRIGGER interaction_context_no_delete BEFORE DELETE ON interaction_context_versions BEGIN SELECT RAISE(ABORT,'immutable context version'); END;
CREATE TABLE interaction_context_selection (
 session_id TEXT PRIMARY KEY REFERENCES interaction_sessions(id), version_id TEXT REFERENCES interaction_context_versions(id)
);
CREATE TABLE interaction_session_operations (
 request_id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, result TEXT NOT NULL
);
