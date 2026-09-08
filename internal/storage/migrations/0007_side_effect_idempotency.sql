CREATE TABLE IF NOT EXISTS side_effect_records (
  idempotency_key text primary key,
  workflow_id text not null,
  node_id text not null,
  capability_id text not null,
  status text not null,
  result_ref text not null,
  created_at timestamp not null,
  updated_at timestamp not null
);

CREATE INDEX IF NOT EXISTS idx_side_effect_records_workflow ON side_effect_records(workflow_id, node_id);
