-- Rebuilt-only metadata. No legacy skill, registry, runtime or retrieval grants.
CREATE TABLE interaction_skill_catalog (
 id TEXT PRIMARY KEY CHECK(length(CAST(id AS BLOB)) BETWEEN 1 AND 128),
 declared_id TEXT NOT NULL UNIQUE CHECK(length(CAST(declared_id AS BLOB)) BETWEEN 1 AND 128),
 generation TEXT NOT NULL CHECK(length(CAST(generation AS BLOB)) BETWEEN 1 AND 128),
 revision INTEGER NOT NULL CHECK(revision>0),
 state TEXT NOT NULL CHECK(state IN ('disabled','enabled')),
 selected_revision TEXT NOT NULL CHECK(length(CAST(selected_revision AS BLOB))<=128),
 CHECK((state='disabled' AND selected_revision='') OR (state='enabled' AND selected_revision<>''))
);
CREATE TRIGGER interaction_skill_catalog_identity BEFORE UPDATE OF id,declared_id,generation ON interaction_skill_catalog BEGIN SELECT RAISE(ABORT,'immutable skill identity'); END;
CREATE TRIGGER interaction_skill_catalog_monotonic BEFORE UPDATE ON interaction_skill_catalog WHEN NEW.revision<>OLD.revision+1 BEGIN SELECT RAISE(ABORT,'nonmonotonic skill revision'); END;
CREATE TRIGGER interaction_skill_catalog_insert BEFORE INSERT ON interaction_skill_catalog WHEN NEW.revision<>1 OR NEW.state<>'disabled' OR EXISTS(SELECT 1 FROM interaction_skill_catalog WHERE id=NEW.id OR declared_id=NEW.declared_id) BEGIN SELECT RAISE(ABORT,'invalid initial skill catalog'); END;
CREATE TRIGGER interaction_skill_catalog_no_delete BEFORE DELETE ON interaction_skill_catalog BEGIN SELECT RAISE(ABORT,'retained skill catalog'); END;
CREATE TABLE interaction_skill_revisions (
 id TEXT PRIMARY KEY CHECK(length(CAST(id AS BLOB)) BETWEEN 1 AND 128),
 catalog_id TEXT NOT NULL REFERENCES interaction_skill_catalog(id),
 declared_version TEXT NOT NULL CHECK(length(CAST(declared_version AS BLOB)) BETWEEN 1 AND 64),
 raw_artifact TEXT NOT NULL CHECK(length(CAST(raw_artifact AS BLOB))<=65536),
 artifact_hash TEXT NOT NULL CHECK(length(artifact_hash)=64),
 canonical_manifest TEXT NOT NULL CHECK(length(CAST(canonical_manifest AS BLOB))<=131072),
 content_hash TEXT NOT NULL CHECK(length(content_hash)=64),
 parser_version TEXT NOT NULL CHECK(length(CAST(parser_version AS BLOB)) BETWEEN 1 AND 64),
 review_version TEXT NOT NULL CHECK(length(CAST(review_version AS BLOB)) BETWEEN 1 AND 64),
 review TEXT NOT NULL CHECK(length(CAST(review AS BLOB))<=262144),
 provenance TEXT NOT NULL CHECK(length(CAST(provenance AS BLOB))<=2048),
 UNIQUE(catalog_id,declared_version)
);
CREATE INDEX interaction_skill_revision_catalog ON interaction_skill_revisions(catalog_id,id);
CREATE TRIGGER interaction_skill_revision_immutable BEFORE UPDATE ON interaction_skill_revisions BEGIN SELECT RAISE(ABORT,'immutable skill revision'); END;
CREATE TRIGGER interaction_skill_revision_no_delete BEFORE DELETE ON interaction_skill_revisions BEGIN SELECT RAISE(ABORT,'retained skill revision'); END;
CREATE TRIGGER interaction_skill_revision_no_replace BEFORE INSERT ON interaction_skill_revisions WHEN EXISTS(SELECT 1 FROM interaction_skill_revisions WHERE id=NEW.id OR (catalog_id=NEW.catalog_id AND declared_version=NEW.declared_version)) BEGIN SELECT RAISE(ABORT,'immutable skill revision'); END;
CREATE TRIGGER interaction_skill_catalog_selection BEFORE UPDATE ON interaction_skill_catalog WHEN NEW.state='enabled' AND NOT EXISTS(SELECT 1 FROM interaction_skill_revisions WHERE id=NEW.selected_revision AND catalog_id=NEW.id) BEGIN SELECT RAISE(ABORT,'invalid skill selection'); END;
CREATE TABLE interaction_skill_operations (
 id TEXT PRIMARY KEY CHECK(length(CAST(id AS BLOB)) BETWEEN 1 AND 128),
 session_id TEXT NOT NULL REFERENCES interaction_sessions(id),
 preview TEXT NOT NULL CHECK(length(CAST(preview AS BLOB))<=524288),
 staged TEXT NOT NULL CHECK(length(CAST(staged AS BLOB))<=65536),
 reserved_bytes INTEGER NOT NULL CHECK(reserved_bytes>=4*(length(CAST(preview AS BLOB))+4096)+length(CAST(staged AS BLOB)) AND reserved_bytes<=67108864),
 state TEXT NOT NULL CHECK(state IN ('pending','committed','no_op','rejected','expired')),
 result TEXT NOT NULL CHECK(length(CAST(result AS BLOB))<=524288)
);
CREATE TRIGGER interaction_skill_intent_immutable BEFORE UPDATE OF id,session_id,preview,staged,reserved_bytes ON interaction_skill_operations BEGIN SELECT RAISE(ABORT,'immutable skill intent'); END;
CREATE TRIGGER interaction_skill_terminal BEFORE UPDATE ON interaction_skill_operations WHEN OLD.state<>'pending' OR NEW.state='pending' BEGIN SELECT RAISE(ABORT,'consumed skill intent'); END;
CREATE TRIGGER interaction_skill_operation_no_delete BEFORE DELETE ON interaction_skill_operations BEGIN SELECT RAISE(ABORT,'retained skill evidence'); END;
CREATE TRIGGER interaction_skill_operation_no_replace BEFORE INSERT ON interaction_skill_operations WHEN EXISTS(SELECT 1 FROM interaction_skill_operations WHERE id=NEW.id) BEGIN SELECT RAISE(ABORT,'retained skill evidence'); END;
CREATE TABLE interaction_skill_requests (
 request_id TEXT PRIMARY KEY CHECK(length(CAST(request_id AS BLOB)) BETWEEN 1 AND 128),
 session_id TEXT NOT NULL REFERENCES interaction_sessions(id),
 operation_id TEXT NOT NULL REFERENCES interaction_skill_operations(id),
 fingerprint TEXT NOT NULL CHECK(length(fingerprint)=64),
 result TEXT NOT NULL CHECK(length(CAST(result AS BLOB))<=524288)
);
CREATE TRIGGER interaction_skill_request_immutable BEFORE UPDATE ON interaction_skill_requests BEGIN SELECT RAISE(ABORT,'immutable skill request'); END;
CREATE TRIGGER interaction_skill_request_no_delete BEFORE DELETE ON interaction_skill_requests BEGIN SELECT RAISE(ABORT,'retained skill request'); END;
CREATE TRIGGER interaction_skill_request_no_replace BEFORE INSERT ON interaction_skill_requests WHEN EXISTS(SELECT 1 FROM interaction_skill_requests WHERE request_id=NEW.request_id) BEGIN SELECT RAISE(ABORT,'retained skill request'); END;
CREATE TABLE interaction_skill_history (
 operation_id TEXT PRIMARY KEY REFERENCES interaction_skill_operations(id),
 catalog_id TEXT NOT NULL REFERENCES interaction_skill_catalog(id),
 generation TEXT NOT NULL CHECK(length(CAST(generation AS BLOB)) BETWEEN 1 AND 128),
 revision INTEGER NOT NULL,
 state TEXT NOT NULL,
 selected_revision TEXT NOT NULL CHECK(length(CAST(selected_revision AS BLOB))<=128),
 UNIQUE(catalog_id,revision)
);
CREATE TRIGGER interaction_skill_history_immutable BEFORE UPDATE ON interaction_skill_history BEGIN SELECT RAISE(ABORT,'immutable skill history'); END;
CREATE TRIGGER interaction_skill_history_no_delete BEFORE DELETE ON interaction_skill_history BEGIN SELECT RAISE(ABORT,'retained skill history'); END;
CREATE TRIGGER interaction_skill_history_no_replace BEFORE INSERT ON interaction_skill_history WHEN EXISTS(SELECT 1 FROM interaction_skill_history WHERE operation_id=NEW.operation_id OR (catalog_id=NEW.catalog_id AND revision=NEW.revision)) BEGIN SELECT RAISE(ABORT,'retained skill history'); END;
