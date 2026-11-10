-- Preserve exact intent for read-only recovery of an uncertain client write.
-- Older completed operations have no recoverable client intent; do not invent it.
ALTER TABLE interaction_session_operations ADD COLUMN session_id TEXT REFERENCES interaction_sessions(id);
ALTER TABLE interaction_session_operations ADD COLUMN intent TEXT;
CREATE INDEX interaction_session_operation_scope ON interaction_session_operations(session_id,request_id);
