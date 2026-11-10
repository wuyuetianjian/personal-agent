-- One checkpoint can reserve exactly one undo action, even if denied/refused.
-- No old approval can regain authority after a later edit.
CREATE TABLE interaction_edit_undos (
 checkpoint_id TEXT PRIMARY KEY REFERENCES interaction_tool_proposals(id),
 run_id TEXT NOT NULL UNIQUE REFERENCES interaction_runs(id)
);
CREATE TRIGGER interaction_edit_undo_immutable BEFORE UPDATE ON interaction_edit_undos BEGIN SELECT RAISE(ABORT,'immutable undo reservation'); END;
CREATE TRIGGER interaction_edit_undo_no_delete BEFORE DELETE ON interaction_edit_undos BEGIN SELECT RAISE(ABORT,'immutable undo reservation'); END;
-- Written immediately before the protected rename under the native edit mutex.
-- A failed/crashed attempt can invalidate a prior checkpoint conservatively.
CREATE TABLE interaction_file_heads (
 scope_id TEXT NOT NULL, path TEXT NOT NULL, proposal_id TEXT NOT NULL REFERENCES interaction_tool_proposals(id),
 PRIMARY KEY(scope_id,path)
);
