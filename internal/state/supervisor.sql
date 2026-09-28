CREATE TABLE supervisor (
    id INTEGER PRIMARY KEY,
    harness TEXT NOT NULL,
    model TEXT NOT NULL,
    effort TEXT NOT NULL,
    argv TEXT NOT NULL,
    session TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('launching', 'running', 'exited', 'interrupted', 'stopped', 'failed')),
    server_generation TEXT NOT NULL DEFAULT '',
    terminal_id TEXT NOT NULL DEFAULT '',
    pane_id TEXT NOT NULL DEFAULT '',
    pid INTEGER NOT NULL DEFAULT 0,
    start_marker TEXT NOT NULL DEFAULT '',
    wake_cursor INTEGER NOT NULL DEFAULT 0,
    reason TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    ended_at TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE supervisor_input (
    id INTEGER PRIMARY KEY,
    body TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 16384),
    created_at TEXT NOT NULL,
    delivered_at TEXT NOT NULL DEFAULT ''
) STRICT;
