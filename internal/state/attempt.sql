CREATE TABLE attempt (
    id INTEGER PRIMARY KEY,
    task_id INTEGER NOT NULL REFERENCES task(id),
    harness TEXT NOT NULL CHECK (harness IN ('claude', 'codex')),
    model TEXT NOT NULL,
    effort TEXT NOT NULL,
    argv TEXT NOT NULL,
    worktree TEXT NOT NULL,
    branch TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('launching', 'running', 'exited', 'stopped', 'interrupted', 'failed')),
    server_generation TEXT NOT NULL DEFAULT '',
    terminal_id TEXT NOT NULL DEFAULT '',
    pane_id TEXT NOT NULL DEFAULT '',
    pid INTEGER NOT NULL DEFAULT 0,
    start_marker TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    ended_at TEXT NOT NULL DEFAULT '',
    cleaned_at TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX attempt_task ON attempt(task_id, id);

CREATE UNIQUE INDEX attempt_live ON attempt(task_id) WHERE status IN ('launching', 'running');
