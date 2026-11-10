CREATE TABLE interaction_sessions (id TEXT PRIMARY KEY, sequence INTEGER NOT NULL DEFAULT 0);
CREATE TABLE interaction_runs (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES interaction_sessions(id),
 request_id TEXT NOT NULL, message TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('running','completed','failed','cancelled')),
 version INTEGER NOT NULL, UNIQUE(session_id,request_id)
);
CREATE UNIQUE INDEX interaction_active_run ON interaction_runs(session_id) WHERE state='running';
CREATE TABLE interaction_events (
 session_id TEXT NOT NULL, run_id TEXT NOT NULL, sequence INTEGER NOT NULL,
 version INTEGER NOT NULL, type TEXT NOT NULL, payload TEXT NOT NULL,
 PRIMARY KEY(session_id,sequence)
);
CREATE TABLE interaction_approvals (
 id TEXT PRIMARY KEY, run_id TEXT NOT NULL REFERENCES interaction_runs(id),
 action_digest TEXT NOT NULL, expires_at INTEGER NOT NULL,
 decision TEXT NOT NULL DEFAULT 'pending' CHECK(decision IN ('pending','approved','denied')),
 consumed_at INTEGER
);
CREATE TRIGGER interaction_approval_immutable BEFORE UPDATE OF action_digest,expires_at,run_id ON interaction_approvals BEGIN SELECT RAISE(ABORT,'immutable approval'); END;
