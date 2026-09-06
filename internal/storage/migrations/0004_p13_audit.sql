CREATE TABLE IF NOT EXISTS security_audit_log (
  id text primary key,
  actor text not null,
  action text not null,
  target text not null,
  status text not null,
  reason text not null,
  evidence_ids text not null,
  created_at timestamp not null
);
CREATE INDEX IF NOT EXISTS idx_security_audit_created ON security_audit_log(created_at);
CREATE INDEX IF NOT EXISTS idx_security_audit_action_status ON security_audit_log(action, status);
