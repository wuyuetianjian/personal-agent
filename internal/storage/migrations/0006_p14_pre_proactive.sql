CREATE TABLE IF NOT EXISTS trigger_history (
  id text primary key,
  trigger_id text not null,
  event_id text not null,
  workflow_id text not null,
  status text not null,
  message text not null,
  created_at timestamp not null
);
CREATE INDEX IF NOT EXISTS idx_trigger_history_trigger ON trigger_history(trigger_id, created_at);

CREATE TABLE IF NOT EXISTS dead_letter_events (
  id text primary key,
  source_id text not null,
  source_type text not null,
  reason text not null,
  payload blob not null,
  created_at timestamp not null
);
CREATE INDEX IF NOT EXISTS idx_dead_letter_events_created ON dead_letter_events(created_at);

CREATE TABLE IF NOT EXISTS notification_inbox (
  id text primary key,
  project_id text not null,
  trigger_id text not null,
  title text not null,
  body text not null,
  dedup_key text not null,
  status text not null,
  created_at timestamp not null,
  read_at timestamp null
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_notification_dedup_key ON notification_inbox(dedup_key) WHERE dedup_key != '';

CREATE TABLE IF NOT EXISTS goals (
  id text primary key,
  project_id text not null,
  title text not null,
  status text not null,
  max_iterations integer not null,
  state_json text not null,
  created_at timestamp not null,
  updated_at timestamp not null
);
CREATE INDEX IF NOT EXISTS idx_goals_project_status ON goals(project_id, status);
