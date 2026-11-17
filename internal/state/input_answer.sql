ALTER TABLE supervisor_input ADD COLUMN answered_at TEXT NOT NULL DEFAULT '';
ALTER TABLE supervisor_input ADD COLUMN reason TEXT NOT NULL DEFAULT '';
ALTER TABLE supervisor_input ADD COLUMN reason_detail TEXT NOT NULL DEFAULT '';
