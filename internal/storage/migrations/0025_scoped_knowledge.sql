-- Trusted workspace scope, not business project metadata. Content is private
-- runtime data. Lifecycle tombstones retain provenance without retrieval grants.
CREATE TABLE interaction_knowledge_documents (
 id TEXT PRIMARY KEY,
 scope_id TEXT NOT NULL,
 source_kind TEXT NOT NULL CHECK(source_kind IN ('text','file')),
 source TEXT NOT NULL,
 content TEXT NOT NULL,
 content_hash TEXT NOT NULL,
 revision TEXT NOT NULL,
 index_revision TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('indexed','empty','deactivated'))
);
CREATE INDEX interaction_knowledge_scope ON interaction_knowledge_documents(scope_id,id);
CREATE TRIGGER interaction_knowledge_identity_immutable BEFORE UPDATE OF id,scope_id,source_kind,source ON interaction_knowledge_documents BEGIN SELECT RAISE(ABORT,'immutable knowledge provenance'); END;
CREATE TRIGGER interaction_knowledge_no_delete BEFORE DELETE ON interaction_knowledge_documents BEGIN SELECT RAISE(ABORT,'retained knowledge provenance'); END;
CREATE TABLE interaction_knowledge_versions (
 operation_id TEXT PRIMARY KEY,
 document_id TEXT NOT NULL REFERENCES interaction_knowledge_documents(id),
 content TEXT NOT NULL,
 content_hash TEXT NOT NULL,
 revision TEXT NOT NULL,
 index_revision TEXT NOT NULL,
 state TEXT NOT NULL
);
CREATE TRIGGER interaction_knowledge_version_immutable BEFORE UPDATE ON interaction_knowledge_versions BEGIN SELECT RAISE(ABORT,'immutable knowledge version'); END;
CREATE TRIGGER interaction_knowledge_version_no_delete BEFORE DELETE ON interaction_knowledge_versions BEGIN SELECT RAISE(ABORT,'retained knowledge version'); END;
CREATE TABLE interaction_knowledge_operations (
 id TEXT PRIMARY KEY,
 session_id TEXT NOT NULL REFERENCES interaction_sessions(id),
 preview TEXT NOT NULL,
 staged TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','committed','no_op','rejected','expired')),
 result TEXT NOT NULL
);
CREATE TRIGGER interaction_knowledge_intent_immutable BEFORE UPDATE OF id,session_id,preview,staged ON interaction_knowledge_operations BEGIN SELECT RAISE(ABORT,'immutable knowledge intent'); END;
CREATE TRIGGER interaction_knowledge_terminal BEFORE UPDATE ON interaction_knowledge_operations WHEN OLD.state<>'pending' BEGIN SELECT RAISE(ABORT,'consumed knowledge intent'); END;
CREATE TRIGGER interaction_knowledge_operation_no_delete BEFORE DELETE ON interaction_knowledge_operations BEGIN SELECT RAISE(ABORT,'retained knowledge evidence'); END;
CREATE TABLE interaction_knowledge_requests (
 request_id TEXT PRIMARY KEY,
 session_id TEXT NOT NULL REFERENCES interaction_sessions(id),
 operation_id TEXT NOT NULL REFERENCES interaction_knowledge_operations(id),
 fingerprint TEXT NOT NULL,
 result TEXT NOT NULL
);
CREATE TRIGGER interaction_knowledge_request_immutable BEFORE UPDATE ON interaction_knowledge_requests BEGIN SELECT RAISE(ABORT,'immutable knowledge request'); END;
CREATE TRIGGER interaction_knowledge_request_no_delete BEFORE DELETE ON interaction_knowledge_requests BEGIN SELECT RAISE(ABORT,'retained knowledge request'); END;
