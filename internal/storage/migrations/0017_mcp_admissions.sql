-- Starting a trusted MCP process can have effects before session binding. Never
-- reuse an admission, including one left unresolved by a crash or failed write.
CREATE TABLE interaction_mcp_admissions (
 request_id TEXT PRIMARY KEY,
 fingerprint TEXT NOT NULL,
 session_id TEXT UNIQUE NOT NULL,
 state TEXT NOT NULL CHECK (state IN ('starting','unresolved','revoked','complete'))
);
