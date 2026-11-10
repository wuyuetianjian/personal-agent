-- Aggregate running remains active for approval, cancellation and reconciliation.
CREATE TABLE interaction_engine (
 run_id TEXT PRIMARY KEY REFERENCES interaction_runs(id),
 owner TEXT NOT NULL DEFAULT '',
 phase TEXT NOT NULL,
 cancel_requested INTEGER NOT NULL DEFAULT 0,
 checkpoint TEXT NOT NULL
);
