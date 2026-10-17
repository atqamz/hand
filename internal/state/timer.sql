CREATE TABLE timer (
    id INTEGER PRIMARY KEY,
    due_at TEXT NOT NULL,
    every INTEGER NOT NULL DEFAULT 0 CHECK (every = 0 OR every >= 60),
    task_id INTEGER REFERENCES task(id),
    note TEXT NOT NULL DEFAULT '' CHECK (length(note) <= 200),
    status TEXT NOT NULL CHECK (status IN ('armed', 'fired', 'cancelled')),
    created_at TEXT NOT NULL,
    ended_at TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX timer_status ON timer(status, task_id);
