ALTER TABLE trigger_state ADD COLUMN previous_state text not null DEFAULT '';
ALTER TABLE trigger_state ADD COLUMN current_state text not null DEFAULT '';
ALTER TABLE trigger_state ADD COLUMN last_transition_at timestamp null;
ALTER TABLE trigger_state ADD COLUMN last_check_at timestamp null;
ALTER TABLE trigger_state ADD COLUMN last_notification_at timestamp null;
