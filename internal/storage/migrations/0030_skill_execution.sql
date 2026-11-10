-- Immutable routing survives missing/tampered deterministic checkpoints and pins.
ALTER TABLE interaction_runs ADD COLUMN execution_kind TEXT NOT NULL DEFAULT 'model' CHECK(execution_kind IN ('model','skill_readonly_v1'));
CREATE TRIGGER interaction_run_kind_immutable BEFORE UPDATE OF execution_kind ON interaction_runs BEGIN SELECT RAISE(ABORT,'immutable execution kind'); END;
CREATE TABLE interaction_skill_run_operations (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES interaction_sessions(id),
 preview TEXT NOT NULL CHECK(length(CAST(preview AS BLOB))<=131072),
 inputs TEXT NOT NULL CHECK(length(CAST(inputs AS BLOB))<=32768),
 reserved_bytes INTEGER NOT NULL CHECK(reserved_bytes>=4*(length(CAST(preview AS BLOB))+4096)+length(CAST(inputs AS BLOB))),
 state TEXT NOT NULL CHECK(state IN ('pending','stale','cancelled','admitted')),
 result TEXT NOT NULL CHECK(length(CAST(result AS BLOB))<=131072)
);
CREATE TRIGGER interaction_skill_run_operation_capacity BEFORE INSERT ON interaction_skill_run_operations WHEN (SELECT count(*) FROM interaction_skill_run_operations)>=1024 OR (SELECT coalesce(sum(reserved_bytes),0) FROM interaction_skill_run_operations)+NEW.reserved_bytes>67108864 BEGIN SELECT RAISE(ABORT,'skill operation capacity'); END;
CREATE TRIGGER interaction_skill_run_operation_immutable BEFORE UPDATE OF id,session_id,preview,inputs,reserved_bytes ON interaction_skill_run_operations BEGIN SELECT RAISE(ABORT,'immutable skill operation'); END;
CREATE TRIGGER interaction_skill_run_operation_terminal BEFORE UPDATE ON interaction_skill_run_operations WHEN OLD.state<>'pending' OR NEW.state='pending' BEGIN SELECT RAISE(ABORT,'terminal skill operation'); END;
CREATE TABLE interaction_skill_run_requests (
 request_id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES interaction_sessions(id),
 operation_id TEXT NOT NULL REFERENCES interaction_skill_run_operations(id),
 fingerprint TEXT NOT NULL CHECK(length(fingerprint)=64),
 result TEXT NOT NULL CHECK(length(CAST(result AS BLOB))<=131072)
);
CREATE TRIGGER interaction_skill_run_request_capacity BEFORE INSERT ON interaction_skill_run_requests WHEN (SELECT count(*) FROM interaction_skill_run_requests)>=2048 BEGIN SELECT RAISE(ABORT,'skill request capacity'); END;
CREATE TABLE interaction_skill_runs (
 run_id TEXT PRIMARY KEY REFERENCES interaction_runs(id), session_id TEXT NOT NULL REFERENCES interaction_sessions(id),
 catalog_id TEXT NOT NULL REFERENCES interaction_skill_catalog(id), revision_id TEXT NOT NULL REFERENCES interaction_skill_revisions(id),
 pin TEXT NOT NULL CHECK(length(CAST(pin AS BLOB))<=8192), pin_hash TEXT NOT NULL CHECK(length(pin_hash)=64),
 inputs TEXT NOT NULL CHECK(length(CAST(inputs AS BLOB))<=32768),
 plan TEXT NOT NULL CHECK(length(CAST(plan AS BLOB))<=65536),
 revoked INTEGER NOT NULL DEFAULT 0 CHECK(revoked IN (0,1)),
 terminal INTEGER NOT NULL DEFAULT 0 CHECK(terminal IN (0,1)),
 retained_bytes INTEGER NOT NULL DEFAULT 0 CHECK(retained_bytes>=0 AND retained_bytes<=8388608),
 reserved_bytes INTEGER NOT NULL DEFAULT 8388608 CHECK(reserved_bytes BETWEEN retained_bytes AND 8388608),
 output_bytes INTEGER NOT NULL DEFAULT 0 CHECK(output_bytes BETWEEN 0 AND 1048576),
 ordinary_events INTEGER NOT NULL DEFAULT 0 CHECK(ordinary_events BETWEEN 0 AND 512),
 control_events INTEGER NOT NULL DEFAULT 0 CHECK(control_events BETWEEN 0 AND 64)
);
CREATE TRIGGER interaction_skill_runs_capacity BEFORE INSERT ON interaction_skill_runs WHEN (SELECT count(*) FROM interaction_skill_runs)>=256 OR (SELECT coalesce(sum(reserved_bytes),0) FROM interaction_skill_runs)+NEW.reserved_bytes>268435456 BEGIN SELECT RAISE(ABORT,'skill execution capacity'); END;
CREATE TRIGGER interaction_skill_runs_immutable BEFORE UPDATE OF run_id,session_id,catalog_id,revision_id,pin,pin_hash,inputs,plan ON interaction_skill_runs BEGIN SELECT RAISE(ABORT,'immutable skill pin'); END;
CREATE TRIGGER interaction_skill_runs_monotonic BEFORE UPDATE ON interaction_skill_runs WHEN NEW.revoked<OLD.revoked OR NEW.terminal<OLD.terminal OR NEW.retained_bytes<OLD.retained_bytes OR NEW.output_bytes<OLD.output_bytes OR NEW.ordinary_events<OLD.ordinary_events OR NEW.control_events<OLD.control_events BEGIN SELECT RAISE(ABORT,'nonmonotonic skill accounting'); END;
CREATE TRIGGER interaction_skill_revoke AFTER UPDATE ON interaction_skill_catalog WHEN NEW.state<>'enabled' OR NEW.selected_revision<>OLD.selected_revision BEGIN UPDATE interaction_skill_runs SET revoked=1 WHERE catalog_id=NEW.id AND terminal=0 AND revoked=0; END;
CREATE TABLE interaction_skill_node_results (
 run_id TEXT NOT NULL REFERENCES interaction_skill_runs(run_id), node_id TEXT NOT NULL, ordinal INTEGER NOT NULL,
 result TEXT NOT NULL CHECK(length(CAST(result AS BLOB))<=131072),
 PRIMARY KEY(run_id,node_id), UNIQUE(run_id,ordinal)
);
CREATE TABLE interaction_skill_output_slots (
 run_id TEXT NOT NULL REFERENCES interaction_skill_runs(run_id), proposal_id TEXT NOT NULL, slot TEXT NOT NULL,
 payload_hash TEXT NOT NULL CHECK(length(payload_hash)=64), bytes INTEGER NOT NULL CHECK(bytes>=0 AND bytes<=1048576),
 PRIMARY KEY(run_id,proposal_id,slot)
);
CREATE TABLE interaction_skill_event_keys (
 run_id TEXT NOT NULL REFERENCES interaction_skill_runs(run_id), identity TEXT NOT NULL, PRIMARY KEY(run_id,identity)
);
CREATE TRIGGER interaction_skill_run_operations_retain BEFORE DELETE ON interaction_skill_run_operations BEGIN SELECT RAISE(ABORT,'retained skill evidence'); END;
CREATE TRIGGER interaction_skill_run_requests_retain BEFORE DELETE ON interaction_skill_run_requests BEGIN SELECT RAISE(ABORT,'retained skill evidence'); END;
CREATE TRIGGER interaction_skill_run_requests_immutable BEFORE UPDATE ON interaction_skill_run_requests BEGIN SELECT RAISE(ABORT,'immutable skill evidence'); END;
CREATE TRIGGER interaction_skill_runs_retain BEFORE DELETE ON interaction_skill_runs BEGIN SELECT RAISE(ABORT,'retained skill evidence'); END;
CREATE TRIGGER interaction_skill_output_slots_retain BEFORE DELETE ON interaction_skill_output_slots BEGIN SELECT RAISE(ABORT,'retained skill evidence'); END;
CREATE TRIGGER interaction_skill_output_slots_immutable BEFORE UPDATE ON interaction_skill_output_slots BEGIN SELECT RAISE(ABORT,'immutable skill evidence'); END;
CREATE TRIGGER interaction_skill_event_keys_retain BEFORE DELETE ON interaction_skill_event_keys BEGIN SELECT RAISE(ABORT,'retained skill evidence'); END;
CREATE TRIGGER interaction_skill_event_keys_immutable BEFORE UPDATE ON interaction_skill_event_keys BEGIN SELECT RAISE(ABORT,'immutable skill evidence'); END;
CREATE TRIGGER interaction_engine_skill_retained_insert AFTER INSERT ON interaction_engine BEGIN UPDATE interaction_skill_runs SET retained_bytes=retained_bytes+(length(CAST(NEW.checkpoint AS BLOB))) WHERE run_id=NEW.run_id; END;
CREATE TRIGGER interaction_engine_skill_retained_update AFTER UPDATE OF checkpoint ON interaction_engine BEGIN UPDATE interaction_skill_runs SET retained_bytes=retained_bytes+max(0,(length(CAST(NEW.checkpoint AS BLOB)))-(length(CAST(OLD.checkpoint AS BLOB)))) WHERE run_id=NEW.run_id; END;
CREATE TRIGGER interaction_tool_proposals_skill_retained_insert AFTER INSERT ON interaction_tool_proposals BEGIN UPDATE interaction_skill_runs SET retained_bytes=retained_bytes+(length(CAST(NEW.id AS BLOB))+length(CAST(NEW.call_id AS BLOB))+length(CAST(NEW.prepared AS BLOB))) WHERE run_id=NEW.run_id; END;
CREATE TRIGGER side_effect_records_skill_retained_insert AFTER INSERT ON side_effect_records BEGIN UPDATE interaction_skill_runs SET retained_bytes=retained_bytes+(length(CAST(NEW.idempotency_key AS BLOB))+length(CAST(NEW.node_id AS BLOB))+length(CAST(NEW.capability_id AS BLOB))+length(CAST(NEW.status AS BLOB))+length(CAST(NEW.result_ref AS BLOB))) WHERE run_id=NEW.workflow_id; END;
CREATE TRIGGER side_effect_records_skill_retained_update AFTER UPDATE OF idempotency_key,node_id,capability_id,status,result_ref ON side_effect_records BEGIN UPDATE interaction_skill_runs SET retained_bytes=retained_bytes+max(0,(length(CAST(NEW.idempotency_key AS BLOB))+length(CAST(NEW.node_id AS BLOB))+length(CAST(NEW.capability_id AS BLOB))+length(CAST(NEW.status AS BLOB))+length(CAST(NEW.result_ref AS BLOB)))-(length(CAST(OLD.idempotency_key AS BLOB))+length(CAST(OLD.node_id AS BLOB))+length(CAST(OLD.capability_id AS BLOB))+length(CAST(OLD.status AS BLOB))+length(CAST(OLD.result_ref AS BLOB)))) WHERE run_id=NEW.workflow_id; END;
CREATE TRIGGER interaction_events_skill_retained_insert AFTER INSERT ON interaction_events BEGIN UPDATE interaction_skill_runs SET retained_bytes=retained_bytes+(length(CAST(NEW.type AS BLOB))+length(CAST(NEW.payload AS BLOB))) WHERE run_id=NEW.run_id; END;
CREATE TRIGGER interaction_skill_node_results_skill_retained_insert AFTER INSERT ON interaction_skill_node_results BEGIN UPDATE interaction_skill_runs SET retained_bytes=retained_bytes+(length(CAST(NEW.node_id AS BLOB))+length(CAST(NEW.result AS BLOB))) WHERE run_id=NEW.run_id; END;
CREATE TRIGGER interaction_skill_node_results_skill_retained_update AFTER UPDATE OF node_id,result ON interaction_skill_node_results BEGIN UPDATE interaction_skill_runs SET retained_bytes=retained_bytes+max(0,(length(CAST(NEW.node_id AS BLOB))+length(CAST(NEW.result AS BLOB)))-(length(CAST(OLD.node_id AS BLOB))+length(CAST(OLD.result AS BLOB)))) WHERE run_id=NEW.run_id; END;
CREATE TRIGGER interaction_skill_output_slots_skill_retained_insert AFTER INSERT ON interaction_skill_output_slots BEGIN UPDATE interaction_skill_runs SET retained_bytes=retained_bytes+(length(CAST(NEW.proposal_id AS BLOB))+length(CAST(NEW.slot AS BLOB))+length(CAST(NEW.payload_hash AS BLOB))+NEW.bytes) WHERE run_id=NEW.run_id; END;
CREATE TRIGGER interaction_skill_event_keys_skill_retained_insert AFTER INSERT ON interaction_skill_event_keys BEGIN UPDATE interaction_skill_runs SET retained_bytes=retained_bytes+(length(CAST(NEW.identity AS BLOB))) WHERE run_id=NEW.run_id; END;
CREATE TRIGGER interaction_skill_runs_no_replace BEFORE INSERT ON interaction_skill_runs WHEN EXISTS(SELECT 1 FROM interaction_skill_runs WHERE run_id=NEW.run_id) BEGIN SELECT RAISE(ABORT,'immutable skill evidence'); END;
CREATE TRIGGER interaction_skill_run_operations_no_replace BEFORE INSERT ON interaction_skill_run_operations WHEN EXISTS(SELECT 1 FROM interaction_skill_run_operations WHERE id=NEW.id) BEGIN SELECT RAISE(ABORT,'immutable skill evidence'); END;
CREATE TRIGGER interaction_skill_run_requests_no_replace BEFORE INSERT ON interaction_skill_run_requests WHEN EXISTS(SELECT 1 FROM interaction_skill_run_requests WHERE request_id=NEW.request_id) BEGIN SELECT RAISE(ABORT,'immutable skill evidence'); END;
CREATE TRIGGER interaction_skill_event_size BEFORE INSERT ON interaction_events WHEN EXISTS(SELECT 1 FROM interaction_skill_runs WHERE run_id=NEW.run_id) AND length(CAST(NEW.payload AS BLOB))>131072 BEGIN SELECT RAISE(ABORT,'skill event payload capacity'); END;

-- Counters live at the insertion boundary as well as in the application's safe
-- error mapping; another event producer cannot bypass the lifetime categories.
CREATE TRIGGER interaction_skill_event_count AFTER INSERT ON interaction_events WHEN EXISTS(SELECT 1 FROM interaction_skill_runs WHERE run_id=NEW.run_id) BEGIN
 UPDATE interaction_skill_runs SET
 ordinary_events=ordinary_events+CASE WHEN NEW.type IN ('run.completed','run.failed','run.cancelled','run.cancelling','run.needs_reconciliation','run.recovered','tool.recovered','tool.uncertain') THEN 0 ELSE 1 END,
 control_events=control_events+CASE WHEN NEW.type IN ('run.completed','run.failed','run.cancelled','run.cancelling','run.needs_reconciliation','run.recovered','tool.recovered','tool.uncertain') THEN 1 ELSE 0 END WHERE run_id=NEW.run_id;
 SELECT CASE WHEN NEW.type IN ('run.recovered','tool.uncertain') AND (SELECT control_events FROM interaction_skill_runs WHERE run_id=NEW.run_id)>60 THEN RAISE(ABORT,'reserved skill terminal evidence') END;
END;

CREATE TABLE interaction_skill_controls (
 request_id TEXT PRIMARY KEY CHECK(length(CAST(request_id AS BLOB)) BETWEEN 1 AND 128),
 run_id TEXT NOT NULL REFERENCES interaction_skill_runs(run_id),
 fingerprint TEXT NOT NULL CHECK(length(fingerprint)=64),
 reserved_bytes INTEGER NOT NULL CHECK(reserved_bytes=4096)
);
CREATE TRIGGER interaction_skill_control_immutable BEFORE UPDATE ON interaction_skill_controls BEGIN SELECT RAISE(ABORT,'immutable skill control reservation'); END;
CREATE TRIGGER interaction_skill_control_retain BEFORE DELETE ON interaction_skill_controls BEGIN SELECT RAISE(ABORT,'retained skill control reservation'); END;
CREATE TRIGGER interaction_skill_control_capacity BEFORE INSERT ON interaction_skill_controls WHEN
 EXISTS(SELECT 1 FROM interaction_skill_controls WHERE request_id=NEW.request_id) OR EXISTS(SELECT 1 FROM interaction_skill_run_requests WHERE request_id=NEW.request_id) OR
 (SELECT count(*) FROM interaction_skill_run_requests)+(SELECT count(*) FROM interaction_skill_controls)>=2048 OR
 (SELECT coalesce(sum(reserved_bytes),0) FROM interaction_skill_run_operations)+(SELECT coalesce(sum(reserved_bytes),0) FROM interaction_skill_controls)+NEW.reserved_bytes>67108864
 BEGIN SELECT RAISE(ABORT,'skill control capacity or identity'); END;
CREATE TRIGGER interaction_skill_request_shared_capacity BEFORE INSERT ON interaction_skill_run_requests WHEN
 EXISTS(SELECT 1 FROM interaction_skill_controls WHERE request_id=NEW.request_id) OR (SELECT count(*) FROM interaction_skill_run_requests)+(SELECT count(*) FROM interaction_skill_controls)>=2048
 BEGIN SELECT RAISE(ABORT,'shared skill request capacity'); END;
CREATE TRIGGER interaction_skill_operation_shared_capacity BEFORE INSERT ON interaction_skill_run_operations WHEN
 (SELECT coalesce(sum(reserved_bytes),0) FROM interaction_skill_run_operations)+(SELECT coalesce(sum(reserved_bytes),0) FROM interaction_skill_controls)+NEW.reserved_bytes>67108864
 BEGIN SELECT RAISE(ABORT,'shared skill operation capacity'); END;
CREATE TRIGGER interaction_skill_control_ack BEFORE INSERT ON interaction_api_actions WHEN
 EXISTS(SELECT 1 FROM interaction_skill_runs WHERE run_id=json_extract(CASE WHEN json_valid(NEW.result) THEN NEW.result ELSE '{}' END,'$.body.run_id')) BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM interaction_skill_controls WHERE request_id=NEW.request_id AND fingerprint=NEW.fingerprint AND run_id=json_extract(CASE WHEN json_valid(NEW.result) THEN NEW.result ELSE '{}' END,'$.body.run_id')) OR length(CAST(NEW.result AS BLOB))>2048 THEN RAISE(ABORT,'unreserved skill control acknowledgment') END;
END;

CREATE TRIGGER interaction_skill_control_ack_immutable BEFORE UPDATE ON interaction_api_actions WHEN EXISTS(SELECT 1 FROM interaction_skill_controls WHERE request_id=OLD.request_id) BEGIN SELECT RAISE(ABORT,'immutable skill control acknowledgment'); END;
CREATE TRIGGER interaction_skill_control_ack_no_replace BEFORE INSERT ON interaction_api_actions WHEN EXISTS(SELECT 1 FROM interaction_skill_controls c JOIN interaction_api_actions a ON a.request_id=c.request_id WHERE c.request_id=NEW.request_id) BEGIN SELECT RAISE(ABORT,'retained skill control acknowledgment'); END;
