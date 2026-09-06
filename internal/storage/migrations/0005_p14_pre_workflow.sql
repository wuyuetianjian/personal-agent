ALTER TABLE workflow_nodes ADD COLUMN dependencies_json text not null DEFAULT '[]';
ALTER TABLE workflow_nodes ADD COLUMN dag_version text not null DEFAULT 'v1';
CREATE INDEX IF NOT EXISTS idx_workflow_nodes_workflow_status ON workflow_nodes(workflow_id, status);
