-- Preserve checkpoint 0022 identities; never infer new execution authorization
-- for previously registered/started intent missing this explicit budget snapshot.
ALTER TABLE interaction_jobs ADD COLUMN execution_policy TEXT NOT NULL DEFAULT '';
UPDATE interaction_jobs SET blocked_reason='legacy_job_execution_policy_unavailable: inspect original intent; never replay unknown execution'
 WHERE blocked_reason='' AND (state='registered' OR EXISTS(SELECT 1 FROM interaction_runs r WHERE r.id=interaction_jobs.run_id AND r.state='running'));
DROP TRIGGER interaction_jobs_immutable;
CREATE TRIGGER interaction_jobs_immutable BEFORE UPDATE OF id,origin_session_id,origin_run_id,session_id,run_id,request_id,content,effects,policy,execution_policy,sequence ON interaction_jobs BEGIN SELECT RAISE(ABORT,'immutable job intent'); END;
