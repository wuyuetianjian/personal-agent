CREATE TABLE IF NOT EXISTS tasks (
  id text primary key,
  title text not null,
  input text not null,
  status text not null,
  created_at timestamp not null,
  updated_at timestamp not null,
  completed_at timestamp null,
  cancelled_at timestamp null,
  leader_model_id text not null,
  privacy_class text not null,
  final_answer text null,
  final_confidence real null,
  error_category text null,
  error_message text null
);

CREATE TABLE IF NOT EXISTS task_nodes (
  id text primary key,
  task_id text not null,
  node_type text not null,
  role text not null,
  input_json text not null,
  status text not null,
  depends_on_json text not null,
  owner_agent_id text null,
  model_id text null,
  timeout_ms integer not null,
  max_attempts integer not null,
  attempt_count integer not null,
  started_at timestamp null,
  completed_at timestamp null,
  error_category text null,
  error_message text null
);

CREATE INDEX IF NOT EXISTS idx_task_nodes_task_id ON task_nodes(task_id);
CREATE INDEX IF NOT EXISTS idx_task_nodes_status ON task_nodes(status);

CREATE TABLE IF NOT EXISTS evidence (
  id text primary key,
  task_id text not null,
  node_id text null,
  type text not null,
  source text not null,
  uri text null,
  content_text text null,
  content_hash text not null,
  metadata_json text not null,
  privacy_class text not null,
  created_at timestamp not null
);

CREATE INDEX IF NOT EXISTS idx_evidence_task_id ON evidence(task_id);
CREATE INDEX IF NOT EXISTS idx_evidence_node_id ON evidence(node_id);
CREATE INDEX IF NOT EXISTS idx_evidence_type ON evidence(type);

CREATE TABLE IF NOT EXISTS claims (
  id text primary key,
  task_id text not null,
  node_id text null,
  claim_text text not null,
  claim_type text not null,
  confidence real not null,
  status text not null,
  created_at timestamp not null
);

CREATE TABLE IF NOT EXISTS claim_evidence (
  claim_id text not null,
  evidence_id text not null,
  support_type text not null,
  score real not null,
  primary key (claim_id, evidence_id)
);

CREATE TABLE IF NOT EXISTS memory_working (
  id text primary key,
  task_id text not null,
  key text not null,
  value_json text not null,
  privacy_class text not null,
  expires_at timestamp not null,
  created_at timestamp not null
);

CREATE TABLE IF NOT EXISTS memory_episodic (
  id text primary key,
  task_id text not null,
  event_type text not null,
  summary text not null,
  payload_json text not null,
  evidence_ids_json text not null,
  confidence real not null,
  privacy_class text not null,
  created_at timestamp not null
);

CREATE INDEX IF NOT EXISTS idx_memory_episodic_task_id ON memory_episodic(task_id);
CREATE INDEX IF NOT EXISTS idx_memory_episodic_created_at ON memory_episodic(created_at);

CREATE TABLE IF NOT EXISTS memory_semantic (
  id text primary key,
  scope text not null,
  subject text not null,
  predicate text not null,
  object text not null,
  source text not null,
  confidence real not null,
  privacy_class text not null,
  evidence_ids_json text not null,
  created_at timestamp not null,
  updated_at timestamp not null
);

CREATE INDEX IF NOT EXISTS idx_memory_semantic_scope ON memory_semantic(scope);
CREATE INDEX IF NOT EXISTS idx_memory_semantic_subject ON memory_semantic(subject);

CREATE TABLE IF NOT EXISTS documents (
  id text primary key,
  source_uri text not null,
  title text not null,
  content_hash text not null,
  metadata_json text not null,
  privacy_class text not null,
  created_at timestamp not null,
  updated_at timestamp not null
);

CREATE TABLE IF NOT EXISTS document_chunks (
  id text primary key,
  document_id text not null,
  chunk_index integer not null,
  text text not null,
  token_count integer not null,
  content_hash text not null,
  metadata_json text not null,
  created_at timestamp not null
);

CREATE INDEX IF NOT EXISTS idx_document_chunks_document_id ON document_chunks(document_id);
CREATE INDEX IF NOT EXISTS idx_document_chunks_content_hash ON document_chunks(content_hash);

CREATE TABLE IF NOT EXISTS model_usage (
  id text primary key,
  task_id text not null,
  node_id text null,
  agent_role text not null,
  provider_id text not null,
  model_id text not null,
  operation text not null,
  input_tokens integer not null,
  output_tokens integer not null,
  billable_units real not null,
  estimated_cost_usd real not null,
  created_at timestamp not null
);

CREATE INDEX IF NOT EXISTS idx_model_usage_task_id ON model_usage(task_id);
CREATE INDEX IF NOT EXISTS idx_model_usage_model_id ON model_usage(model_id);

CREATE TABLE IF NOT EXISTS permission_decisions (
  id text primary key,
  task_id text not null,
  node_id text null,
  action text not null,
  target text not null,
  decision text not null,
  risk_level text not null,
  reason text not null,
  confirmation_id text null,
  created_at timestamp not null
);

CREATE TABLE IF NOT EXISTS browser_sessions (
  id text primary key,
  profile_key text not null,
  runtime text not null,
  allowed_domains_json text not null,
  expires_at timestamp not null,
  reuse_policy text not null,
  isolation_policy text not null,
  storage_policy text not null,
  created_at timestamp not null,
  updated_at timestamp not null
);

