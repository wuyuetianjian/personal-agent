-- Queue ownership is a run admission, not another executor/lease.
CREATE TABLE interaction_queue (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT UNIQUE NOT NULL,
 session_id TEXT NOT NULL REFERENCES interaction_sessions(id),
 request_id TEXT UNIQUE NOT NULL,
 content TEXT NOT NULL,
 policy TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','admitted','withdrawn')),
 run_id TEXT UNIQUE REFERENCES interaction_runs(id),
 blocked_reason TEXT NOT NULL DEFAULT '',
 CHECK((state='admitted' AND run_id IS NOT NULL) OR (state<>'admitted' AND run_id IS NULL))
);
CREATE INDEX interaction_queue_pending ON interaction_queue(session_id,state,sequence);
CREATE TRIGGER interaction_queue_immutable BEFORE UPDATE OF id,session_id,request_id,content,policy,sequence ON interaction_queue BEGIN SELECT RAISE(ABORT,'immutable queued intent'); END;
CREATE TRIGGER interaction_queue_terminal BEFORE UPDATE ON interaction_queue WHEN OLD.state<>'pending' BEGIN SELECT RAISE(ABORT,'queue already claimed or withdrawn'); END;
CREATE TABLE interaction_queue_operations (
 request_id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES interaction_sessions(id),
 kind TEXT NOT NULL, item_id TEXT NOT NULL REFERENCES interaction_queue(id), content TEXT NOT NULL
);
