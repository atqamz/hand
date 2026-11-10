ALTER TABLE task ADD COLUMN parent_id INTEGER REFERENCES task(id);
CREATE INDEX task_parent ON task(parent_id) WHERE parent_id IS NOT NULL;
