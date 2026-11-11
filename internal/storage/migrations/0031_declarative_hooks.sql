-- Declarative policy management has a separate, fixed lifetime pool. A prepared
-- operation reserves both request slots and its possible immutable revision.
CREATE TABLE interaction_hook_sessions (
 session_id TEXT PRIMARY KEY REFERENCES interaction_sessions(id),
 generation INTEGER NOT NULL CHECK(generation>=0),
 state TEXT NOT NULL CHECK(state IN ('disabled','enabled')),
 revision_id TEXT NOT NULL,
 CHECK((state='disabled' AND revision_id='') OR (state='enabled' AND revision_id<>'')),
 CHECK(length(CAST(session_id AS BLOB))+length(CAST(revision_id AS BLOB))+64<=1024)
);
CREATE TABLE interaction_hook_operations (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES interaction_hook_sessions(session_id),
 kind TEXT NOT NULL CHECK(kind IN ('import','enable','disable')),
 revision_id TEXT NOT NULL, expires_at_ms INTEGER NOT NULL,
 preview TEXT NOT NULL CHECK(json_valid(preview) AND length(CAST(preview AS BLOB))<=16384),
 state TEXT NOT NULL CHECK(state IN ('pending','applied','cancelled','stale')),
 result TEXT NOT NULL CHECK(json_valid(result) AND length(CAST(result AS BLOB))<=16384),
 reserved_bytes INTEGER NOT NULL CHECK(reserved_bytes=65536),
 CHECK(json_extract(preview,'$.operation_id')=id AND json_extract(preview,'$.session_id')=session_id AND json_extract(preview,'$.kind')=kind AND json_extract(preview,'$.revision_id')=revision_id AND json_extract(preview,'$.expires_at_ms')=expires_at_ms),
 CHECK(json_extract(result,'$.operation_id')=id AND json_extract(result,'$.session_id')=session_id AND json_extract(result,'$.state')=state)
);
CREATE TABLE interaction_hook_revisions (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES interaction_hook_sessions(session_id),
 policy TEXT NOT NULL CHECK(json_valid(policy) AND length(CAST(policy AS BLOB))<=8192),
 policy_hash TEXT NOT NULL CHECK(length(policy_hash)=64), evaluator TEXT NOT NULL CHECK(evaluator='declarative-hooks-v1'),
 operation_id TEXT NOT NULL UNIQUE REFERENCES interaction_hook_operations(id)
);
CREATE TABLE interaction_hook_requests (
 request_id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES interaction_hook_sessions(session_id),
 operation_id TEXT NOT NULL REFERENCES interaction_hook_operations(id),
 slot INTEGER NOT NULL CHECK(slot IN (0,1)), fingerprint TEXT NOT NULL CHECK(length(fingerprint)=64),
 result TEXT NOT NULL CHECK(result='' OR (json_valid(result) AND length(CAST(result AS BLOB))<=16384)),
 UNIQUE(operation_id,slot),
 CHECK(length(CAST(request_id AS BLOB))+length(CAST(session_id AS BLOB))+length(CAST(operation_id AS BLOB))+length(fingerprint)+64<=1024)
);
CREATE TABLE interaction_hook_audits (
 operation_id TEXT PRIMARY KEY REFERENCES interaction_hook_operations(id),
 session_id TEXT NOT NULL REFERENCES interaction_hook_sessions(session_id),
 result TEXT NOT NULL CHECK(json_valid(result) AND length(CAST(result AS BLOB))<=2048)
);
CREATE TRIGGER interaction_hook_session_capacity BEFORE INSERT ON interaction_hook_sessions WHEN
 EXISTS(SELECT 1 FROM interaction_hook_sessions WHERE session_id=NEW.session_id) OR (SELECT count(*) FROM interaction_hook_sessions)>=128
 BEGIN SELECT RAISE(ABORT,'hook session capacity'); END;
CREATE TRIGGER interaction_hook_session_transition BEFORE UPDATE ON interaction_hook_sessions BEGIN
 SELECT CASE WHEN NEW.session_id<>OLD.session_id OR NEW.generation<>OLD.generation+1 OR (NEW.state=OLD.state AND NEW.revision_id=OLD.revision_id) OR NOT EXISTS(SELECT 1 FROM interaction_hook_operations WHERE session_id=NEW.session_id AND state='pending') OR (NEW.state='enabled' AND NOT EXISTS(SELECT 1 FROM interaction_hook_revisions WHERE id=NEW.revision_id AND session_id=NEW.session_id)) THEN RAISE(ABORT,'hook selection identity') END;
END;
CREATE TRIGGER interaction_hook_session_revoke AFTER UPDATE ON interaction_hook_sessions BEGIN
 UPDATE interaction_skill_runs SET revoked=1 WHERE session_id=NEW.session_id AND terminal=0 AND revoked=0;
END;
CREATE TRIGGER interaction_hook_operation_capacity BEFORE INSERT ON interaction_hook_operations BEGIN
 SELECT CASE WHEN EXISTS(SELECT 1 FROM interaction_hook_operations WHERE id=NEW.id) OR NEW.state<>'pending' OR (SELECT count(*) FROM interaction_hook_operations)>=256 OR (SELECT count(*) FROM interaction_hook_operations WHERE session_id=NEW.session_id)>=64 OR (SELECT coalesce(sum(reserved_bytes),0) FROM interaction_hook_operations)+NEW.reserved_bytes>16777216 OR (SELECT count(*) FROM interaction_hook_operations WHERE state='pending' AND expires_at_ms>CAST(unixepoch('subsec')*1000 AS INTEGER))>=8 OR (SELECT count(*) FROM interaction_hook_operations WHERE session_id=NEW.session_id AND state='pending' AND expires_at_ms>CAST(unixepoch('subsec')*1000 AS INTEGER))>=4 THEN RAISE(ABORT,'hook operation capacity') END;
 SELECT CASE WHEN NEW.kind='import' AND ((SELECT count(*) FROM interaction_hook_revisions)+(SELECT count(*) FROM interaction_hook_operations WHERE kind='import' AND state='pending')>=128 OR (SELECT count(*) FROM interaction_hook_revisions WHERE session_id=NEW.session_id)+(SELECT count(*) FROM interaction_hook_operations WHERE session_id=NEW.session_id AND kind='import' AND state='pending')>=32) THEN RAISE(ABORT,'hook revision reservation capacity') END;
END;
CREATE TRIGGER interaction_hook_operation_transition BEFORE UPDATE ON interaction_hook_operations BEGIN
 SELECT CASE WHEN NEW.id<>OLD.id OR NEW.session_id<>OLD.session_id OR NEW.kind<>OLD.kind OR NEW.revision_id<>OLD.revision_id OR NEW.expires_at_ms<>OLD.expires_at_ms OR NEW.preview<>OLD.preview OR NEW.reserved_bytes<>OLD.reserved_bytes OR OLD.state<>'pending' OR NEW.state='pending' THEN RAISE(ABORT,'immutable hook operation') END;
END;
CREATE TRIGGER interaction_hook_revision_capacity BEFORE INSERT ON interaction_hook_revisions BEGIN
 SELECT CASE WHEN EXISTS(SELECT 1 FROM interaction_hook_revisions WHERE id=NEW.id) OR (SELECT count(*) FROM interaction_hook_revisions)>=128 OR (SELECT count(*) FROM interaction_hook_revisions WHERE session_id=NEW.session_id)>=32 OR NOT EXISTS(SELECT 1 FROM interaction_hook_operations WHERE id=NEW.operation_id AND session_id=NEW.session_id AND revision_id=NEW.id AND kind='import' AND state='pending' AND json_extract(preview,'$.policy_sha256')=NEW.policy_hash) THEN RAISE(ABORT,'hook revision capacity or identity') END;
END;
CREATE TRIGGER interaction_hook_request_capacity BEFORE INSERT ON interaction_hook_requests BEGIN
 SELECT CASE WHEN EXISTS(SELECT 1 FROM interaction_hook_requests WHERE request_id=NEW.request_id) OR (SELECT count(*) FROM interaction_hook_requests)>=512 OR (SELECT count(*) FROM interaction_hook_requests WHERE session_id=NEW.session_id)>=128 OR NOT EXISTS(SELECT 1 FROM interaction_hook_operations WHERE id=NEW.operation_id AND session_id=NEW.session_id) OR (NEW.slot=0 AND (NEW.request_id<>NEW.operation_id OR NEW.result<>'')) OR (NEW.slot=1 AND (NEW.request_id=NEW.operation_id OR NEW.result='' OR NEW.result<>(SELECT result FROM interaction_hook_operations WHERE id=NEW.operation_id))) THEN RAISE(ABORT,'hook request capacity or identity') END;
END;
CREATE TRIGGER interaction_hook_audit_identity BEFORE INSERT ON interaction_hook_audits WHEN EXISTS(SELECT 1 FROM interaction_hook_audits WHERE operation_id=NEW.operation_id) OR NOT EXISTS(SELECT 1 FROM interaction_hook_operations WHERE id=NEW.operation_id AND session_id=NEW.session_id AND state<>'pending') BEGIN SELECT RAISE(ABORT,'hook audit identity'); END;
CREATE TRIGGER interaction_hook_sessions_retain BEFORE DELETE ON interaction_hook_sessions BEGIN SELECT RAISE(ABORT,'retained hook evidence'); END;
CREATE TRIGGER interaction_hook_operations_retain BEFORE DELETE ON interaction_hook_operations BEGIN SELECT RAISE(ABORT,'retained hook evidence'); END;
CREATE TRIGGER interaction_hook_revisions_retain BEFORE DELETE ON interaction_hook_revisions BEGIN SELECT RAISE(ABORT,'retained hook evidence'); END;
CREATE TRIGGER interaction_hook_revisions_immutable BEFORE UPDATE ON interaction_hook_revisions BEGIN SELECT RAISE(ABORT,'immutable hook evidence'); END;
CREATE TRIGGER interaction_hook_requests_retain BEFORE DELETE ON interaction_hook_requests BEGIN SELECT RAISE(ABORT,'retained hook evidence'); END;
CREATE TRIGGER interaction_hook_requests_immutable BEFORE UPDATE ON interaction_hook_requests BEGIN SELECT RAISE(ABORT,'immutable hook evidence'); END;
CREATE TRIGGER interaction_hook_audits_retain BEFORE DELETE ON interaction_hook_audits BEGIN SELECT RAISE(ABORT,'retained hook evidence'); END;
CREATE TRIGGER interaction_hook_audits_immutable BEFORE UPDATE ON interaction_hook_audits BEGIN SELECT RAISE(ABORT,'immutable hook evidence'); END;

-- A complete immutable Hook slab is precharged within the existing execution
-- pool. No receipt can reopen a released terminal reservation.
CREATE TABLE interaction_skill_run_hooks (
 run_id TEXT PRIMARY KEY REFERENCES interaction_skill_runs(run_id), session_id TEXT NOT NULL REFERENCES interaction_sessions(id),
 snapshot TEXT NOT NULL CHECK(json_valid(snapshot)), snapshot_hash TEXT NOT NULL CHECK(length(snapshot_hash)=64), pin_hash TEXT NOT NULL CHECK(length(pin_hash)=64),
 reserved_bytes INTEGER NOT NULL CHECK(reserved_bytes=163840),
 CHECK(length(CAST(run_id AS BLOB))+length(CAST(session_id AS BLOB))+length(CAST(snapshot AS BLOB))+128+64<=12288)
);
CREATE TRIGGER interaction_skill_hook_snapshot_insert BEFORE INSERT ON interaction_skill_run_hooks BEGIN
 SELECT CASE WHEN EXISTS(SELECT 1 FROM interaction_skill_run_hooks WHERE run_id=NEW.run_id) OR NOT EXISTS(SELECT 1 FROM interaction_skill_runs WHERE run_id=NEW.run_id AND session_id=NEW.session_id AND pin_hash=NEW.pin_hash AND json_extract(pin,'$.schema_version')=2 AND json_extract(pin,'$.hooks.snapshot_sha256')=NEW.snapshot_hash AND terminal=0 AND retained_bytes>=163840 AND reserved_bytes=8388608) THEN RAISE(ABORT,'hook snapshot identity or reservation') END;
END;
CREATE TRIGGER interaction_skill_hook_snapshot_immutable BEFORE UPDATE ON interaction_skill_run_hooks BEGIN SELECT RAISE(ABORT,'immutable hook snapshot'); END;
CREATE TRIGGER interaction_skill_hook_snapshot_retain BEFORE DELETE ON interaction_skill_run_hooks BEGIN SELECT RAISE(ABORT,'retained hook snapshot'); END;
CREATE TABLE interaction_skill_hook_receipts (
 run_id TEXT NOT NULL REFERENCES interaction_skill_run_hooks(run_id),session_id TEXT NOT NULL REFERENCES interaction_sessions(id),
 event TEXT NOT NULL CHECK(event IN ('before_node','after_node','run_terminal')),node_id TEXT NOT NULL,ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 0 AND 16),
 receipt_id TEXT NOT NULL UNIQUE CHECK(length(receipt_id)=64),proposal_id TEXT NOT NULL,proposal_digest TEXT NOT NULL,snapshot_hash TEXT NOT NULL CHECK(length(snapshot_hash)=64),
 payload TEXT NOT NULL CHECK(json_valid(payload)),row_bytes INTEGER NOT NULL CHECK(row_bytes BETWEEN 1 AND 4096),
 PRIMARY KEY(run_id,event,node_id),
 CHECK(row_bytes=length(CAST(run_id AS BLOB))+length(CAST(session_id AS BLOB))+length(event)+length(CAST(node_id AS BLOB))+length(receipt_id)+length(CAST(proposal_id AS BLOB))+length(proposal_digest)+length(snapshot_hash)+length(CAST(payload AS BLOB))+128),
 CHECK((event='run_terminal' AND node_id='' AND ordinal=16 AND proposal_id='' AND proposal_digest='') OR (event<>'run_terminal' AND node_id<>'' AND ordinal<16 AND proposal_id<>'' AND length(proposal_digest)=64)),
 CHECK(coalesce(json_extract(payload,'$.run_id')=run_id AND json_extract(payload,'$.session_id')=session_id AND json_extract(payload,'$.event')=event AND json_extract(payload,'$.node_id')=node_id AND json_extract(payload,'$.receipt_id')=receipt_id AND json_extract(payload,'$.proposal_id')=proposal_id AND json_extract(payload,'$.proposal_digest')=proposal_digest AND json_extract(payload,'$.snapshot_sha256')=snapshot_hash,0))
);
CREATE TRIGGER interaction_skill_hook_receipt_capacity BEFORE INSERT ON interaction_skill_hook_receipts BEGIN
 SELECT CASE WHEN EXISTS(SELECT 1 FROM interaction_skill_hook_receipts WHERE run_id=NEW.run_id AND event=NEW.event AND node_id=NEW.node_id) OR EXISTS(SELECT 1 FROM interaction_skill_hook_receipts WHERE receipt_id=NEW.receipt_id) OR (SELECT count(*) FROM interaction_skill_hook_receipts WHERE run_id=NEW.run_id)>=33 OR NOT EXISTS(SELECT 1 FROM interaction_skill_run_hooks h JOIN interaction_skill_runs s ON s.run_id=h.run_id WHERE h.run_id=NEW.run_id AND h.session_id=NEW.session_id AND h.snapshot_hash=NEW.snapshot_hash AND s.terminal=0) OR (NEW.event<>'run_terminal' AND NOT EXISTS(SELECT 1 FROM interaction_skill_node_results WHERE run_id=NEW.run_id AND node_id=NEW.node_id AND ordinal=NEW.ordinal)) THEN RAISE(ABORT,'hook receipt capacity or identity') END;
END;
CREATE TRIGGER interaction_skill_hook_receipts_immutable BEFORE UPDATE ON interaction_skill_hook_receipts BEGIN SELECT RAISE(ABORT,'immutable hook receipt'); END;
CREATE TRIGGER interaction_skill_hook_receipts_retain BEFORE DELETE ON interaction_skill_hook_receipts BEGIN SELECT RAISE(ABORT,'retained hook receipt'); END;
CREATE TABLE interaction_skill_hook_stops (
 run_id TEXT PRIMARY KEY REFERENCES interaction_skill_run_hooks(run_id),node_id TEXT NOT NULL,proposal_id TEXT NOT NULL,proposal_digest TEXT NOT NULL CHECK(length(proposal_digest)=64),
 receipt_id TEXT NOT NULL REFERENCES interaction_skill_hook_receipts(receipt_id),reason TEXT NOT NULL CHECK(reason IN ('hook_denied','hook_evaluation_failed')),
 CHECK(length(CAST(run_id AS BLOB))+length(CAST(node_id AS BLOB))+length(CAST(proposal_id AS BLOB))+length(proposal_digest)+length(receipt_id)+length(reason)+256<=16384)
);
CREATE TRIGGER interaction_skill_hook_stop_insert BEFORE INSERT ON interaction_skill_hook_stops BEGIN
 SELECT CASE WHEN EXISTS(SELECT 1 FROM interaction_skill_hook_stops WHERE run_id=NEW.run_id) OR NOT EXISTS(SELECT 1 FROM interaction_skill_hook_receipts WHERE run_id=NEW.run_id AND node_id=NEW.node_id AND proposal_id=NEW.proposal_id AND proposal_digest=NEW.proposal_digest AND receipt_id=NEW.receipt_id AND event='before_node' AND json_extract(payload,'$.decision') IN ('deny','evaluation_error')) OR EXISTS(SELECT 1 FROM side_effect_records WHERE workflow_id=NEW.run_id) AND EXISTS(SELECT 1 FROM side_effect_records WHERE workflow_id=NEW.run_id AND node_id=NEW.proposal_id) OR NOT EXISTS(SELECT 1 FROM interaction_tool_proposals WHERE id=NEW.proposal_id AND run_id=NEW.run_id AND executed=0) THEN RAISE(ABORT,'hook stop identity') END;
END;
CREATE TRIGGER interaction_skill_hook_stops_immutable BEFORE UPDATE ON interaction_skill_hook_stops BEGIN SELECT RAISE(ABORT,'immutable hook stop'); END;
CREATE TRIGGER interaction_skill_hook_stops_retain BEFORE DELETE ON interaction_skill_hook_stops BEGIN SELECT RAISE(ABORT,'retained hook stop'); END;
CREATE TRIGGER interaction_skill_hook_reservation_closed BEFORE UPDATE OF reserved_bytes ON interaction_skill_runs WHEN OLD.terminal=1 AND NEW.reserved_bytes>OLD.reserved_bytes AND EXISTS(SELECT 1 FROM interaction_skill_run_hooks WHERE run_id=OLD.run_id) BEGIN SELECT RAISE(ABORT,'closed hook execution reservation'); END;
