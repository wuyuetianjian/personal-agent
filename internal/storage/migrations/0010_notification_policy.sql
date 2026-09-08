ALTER TABLE notification_inbox ADD COLUMN severity text not null DEFAULT 'info';
ALTER TABLE notification_inbox ADD COLUMN delivery_state text not null DEFAULT 'inbox';
ALTER TABLE notification_inbox ADD COLUMN delivery_after timestamp null;
