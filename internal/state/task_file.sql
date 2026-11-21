CREATE TABLE task_file (
    task_id INTEGER NOT NULL REFERENCES task(id),
    name TEXT NOT NULL,
    added_at TEXT NOT NULL,
    PRIMARY KEY (task_id, name)
) STRICT;

CREATE INDEX task_file_name ON task_file(name);
