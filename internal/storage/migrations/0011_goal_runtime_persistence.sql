ALTER TABLE goals ADD COLUMN completion_criteria text not null DEFAULT '';
ALTER TABLE goals ADD COLUMN budget_policy text not null DEFAULT '';
ALTER TABLE goals ADD COLUMN workflow_id text not null DEFAULT '';
ALTER TABLE goals ADD COLUMN last_evaluated_at timestamp null;

CREATE TABLE IF NOT EXISTS goal_milestones (
  id text primary key,
  goal_id text not null,
  title text not null,
  status text not null,
  dependencies_json text not null,
  workflow_id text not null,
  completion_criteria text not null,
  order_index integer not null,
  created_at timestamp not null,
  updated_at timestamp not null
);

CREATE INDEX IF NOT EXISTS idx_goal_milestones_goal_order ON goal_milestones(goal_id, order_index);
CREATE INDEX IF NOT EXISTS idx_goal_milestones_status ON goal_milestones(status);
