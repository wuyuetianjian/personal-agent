CREATE TABLE interaction_jobs (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT UNIQUE NOT NULL,
 origin_session_id TEXT NOT NULL REFERENCES interaction_sessions(id),
 origin_run_id TEXT NOT NULL,
 session_id TEXT UNIQUE NOT NULL REFERENCES interaction_sessions(id),
 run_id TEXT UNIQUE NOT NULL,
 request_id TEXT UNIQUE NOT NULL,
 content TEXT NOT NULL,
 effects INTEGER NOT NULL,
 policy TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('registered','admitted','stopped')),
 stop_requested INTEGER NOT NULL DEFAULT 0,
 blocked_reason TEXT NOT NULL DEFAULT ''
);
CREATE TRIGGER interaction_jobs_immutable BEFORE UPDATE OF id,origin_session_id,origin_run_id,session_id,run_id,request_id,content,effects,policy,sequence ON interaction_jobs BEGIN SELECT RAISE(ABORT,'immutable job intent'); END;
CREATE TABLE interaction_job_operations (
 request_id TEXT PRIMARY KEY, session_id TEXT NOT NULL, job_id TEXT NOT NULL REFERENCES interaction_jobs(id),
 kind TEXT NOT NULL, payload TEXT NOT NULL
);
