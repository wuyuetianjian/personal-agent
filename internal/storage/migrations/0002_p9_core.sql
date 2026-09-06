CREATE TABLE IF NOT EXISTS projects (
  id text primary key, name text not null, privacy_class text not null,
  repository_refs_json text not null, knowledge_scopes_json text not null,
  memory_scope text not null, allowed_skills_json text not null,
  allowed_capabilities_json text not null, allowed_coding_agents_json text not null,
  budget_policy text not null
);

CREATE TABLE IF NOT EXISTS skills (
  id text primary key, name text not null, description text not null,
  active_version text not null, status text not null, source text not null,
  created_at timestamp not null, updated_at timestamp not null
);

CREATE TABLE IF NOT EXISTS skill_versions (
  skill_id text not null, version text not null, manifest_json text not null,
  checksum text not null, created_at timestamp not null,
  primary key (skill_id, version)
);

CREATE TABLE IF NOT EXISTS workflow_runs (
  id text primary key, task_id text not null, project_id text not null,
  skill_id text not null, skill_version text not null, status text not null,
  input_json text not null, result_json text not null, started_at timestamp not null,
  updated_at timestamp not null, completed_at timestamp null
);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_status ON workflow_runs(status);

CREATE TABLE IF NOT EXISTS workflow_nodes (
  workflow_id text not null, node_id text not null, capability_id text not null,
  role text not null, status text not null, attempt integer not null,
  idempotency_key text not null, side_effect boolean not null,
  started_at timestamp null, completed_at timestamp null,
  primary key (workflow_id, node_id)
);
CREATE INDEX IF NOT EXISTS idx_workflow_nodes_status ON workflow_nodes(status);

CREATE TABLE IF NOT EXISTS workflow_checkpoints (
  id integer primary key autoincrement, workflow_id text not null,
  node_id text not null, status text not null, evidence_ids_json text not null,
  result_ref text not null, usage_json text not null, created_at timestamp not null
);
CREATE INDEX IF NOT EXISTS idx_workflow_checkpoints_workflow ON workflow_checkpoints(workflow_id, created_at);
