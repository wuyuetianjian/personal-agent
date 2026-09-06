CREATE TABLE IF NOT EXISTS triggers (
  id text primary key,
  project_id text not null,
  type text not null,
  enabled boolean not null,
  workflow_template_id text not null,
  skill_id text not null,
  schedule_json text not null,
  event_json text not null,
  condition_json text not null,
  policy_ref text not null,
  budget_ref text not null,
  created_at timestamp not null,
  updated_at timestamp not null
);
CREATE INDEX IF NOT EXISTS idx_triggers_project ON triggers(project_id);
CREATE INDEX IF NOT EXISTS idx_triggers_enabled ON triggers(enabled, type);

CREATE TABLE IF NOT EXISTS trigger_state (
  trigger_id text primary key,
  last_fired_at timestamp null,
  last_success_at timestamp null,
  last_failure_at timestamp null,
  next_fire_at timestamp null,
  last_event_hash text not null,
  consecutive_failures integer not null,
  cooldown_until timestamp null,
  state_json text not null
);
CREATE INDEX IF NOT EXISTS idx_trigger_state_next_fire ON trigger_state(next_fire_at);

CREATE TABLE IF NOT EXISTS events (
  id text primary key,
  source text not null,
  type text not null,
  project_id text not null,
  payload blob not null,
  privacy_class text not null,
  trust_level text not null,
  occurred_at timestamp not null,
  received_at timestamp not null,
  dedup_key text not null,
  correlation_id text not null,
  causation_id text not null,
  event_depth integer not null
);
CREATE INDEX IF NOT EXISTS idx_events_project_received ON events(project_id, received_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_events_dedup_key ON events(dedup_key) WHERE dedup_key != '';
