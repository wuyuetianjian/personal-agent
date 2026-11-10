-- Local metadata only. No execution owner, tool permission or policy changes.
CREATE TABLE interaction_operator_operations (
 id TEXT PRIMARY KEY,
 session_id TEXT NOT NULL REFERENCES interaction_sessions(id),
 preview TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','committed','no_op','rejected','expired')),
 result TEXT NOT NULL
);
CREATE TRIGGER interaction_operator_intent_immutable BEFORE UPDATE OF id,session_id,preview ON interaction_operator_operations BEGIN SELECT RAISE(ABORT,'immutable operator intent'); END;
CREATE TRIGGER interaction_operator_terminal BEFORE UPDATE ON interaction_operator_operations WHEN OLD.state<>'pending' BEGIN SELECT RAISE(ABORT,'consumed operator intent'); END;
CREATE TRIGGER interaction_operator_no_delete BEFORE DELETE ON interaction_operator_operations BEGIN SELECT RAISE(ABORT,'retained operator evidence'); END;
CREATE TABLE interaction_operator_requests (
 request_id TEXT PRIMARY KEY,
 session_id TEXT NOT NULL REFERENCES interaction_sessions(id),
 operation_id TEXT NOT NULL REFERENCES interaction_operator_operations(id),
 fingerprint TEXT NOT NULL,
 result TEXT NOT NULL
);
CREATE TRIGGER interaction_operator_request_immutable BEFORE UPDATE ON interaction_operator_requests BEGIN SELECT RAISE(ABORT,'immutable operator request'); END;
CREATE TRIGGER interaction_operator_request_no_delete BEFORE DELETE ON interaction_operator_requests BEGIN SELECT RAISE(ABORT,'retained operator request'); END;
