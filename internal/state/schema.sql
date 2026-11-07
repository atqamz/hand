CREATE TABLE project (
    name TEXT PRIMARY KEY,
    repo TEXT NOT NULL,
    created_at TEXT NOT NULL,
    settings TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE task (
    id INTEGER PRIMARY KEY,
    project TEXT NOT NULL REFERENCES project(name),
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    goal TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('inbox', 'active', 'done', 'abandoned')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE INDEX task_status ON task(status, id);

CREATE TABLE plan (
    task_id INTEGER NOT NULL REFERENCES task(id),
    revision INTEGER NOT NULL CHECK (revision >= 1),
    body TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (task_id, revision)
) STRICT;

CREATE TABLE decision (
    id INTEGER PRIMARY KEY,
    task_id INTEGER NOT NULL REFERENCES task(id),
    question TEXT NOT NULL CHECK (length(question) >= 1),
    status TEXT NOT NULL CHECK (status IN ('open', 'answered', 'withdrawn')),
    answer TEXT NOT NULL DEFAULT '',
    answered_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    closed_at TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX decision_status ON decision(status, task_id, id);

CREATE TABLE event (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    at TEXT NOT NULL,
    kind TEXT NOT NULL,
    task_id INTEGER,
    detail TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE attempt (
    id INTEGER PRIMARY KEY,
    task_id INTEGER NOT NULL REFERENCES task(id),
    harness TEXT NOT NULL,
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
    cleaned_at TEXT NOT NULL DEFAULT '',
    continues INTEGER NOT NULL DEFAULT 0,
    screen TEXT,
    approve TEXT NOT NULL DEFAULT 'never' CHECK (approve IN ('never', 'ask')),
    access TEXT NOT NULL DEFAULT 'open' CHECK (access IN ('open', 'readonly'))
) STRICT;

CREATE INDEX attempt_task ON attempt(task_id, id);

CREATE UNIQUE INDEX attempt_live ON attempt(task_id) WHERE status IN ('launching', 'running');

CREATE TABLE report (
    id INTEGER PRIMARY KEY,
    attempt_id INTEGER NOT NULL REFERENCES attempt(id),
    task_id INTEGER NOT NULL REFERENCES task(id),
    status TEXT NOT NULL CHECK (status IN ('progress', 'done', 'stuck')),
    body TEXT NOT NULL CHECK (length(body) >= 1),
    created_at TEXT NOT NULL,
    acked_at TEXT NOT NULL DEFAULT '',
    acked_by TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX report_attempt ON report(attempt_id, id);

CREATE INDEX report_task ON report(task_id, id);

CREATE INDEX report_unacked ON report(acked_at, id);

CREATE TABLE fleet (
    only INTEGER PRIMARY KEY CHECK (only = 1),
    id TEXT NOT NULL,
    name TEXT NOT NULL
) STRICT;

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
    ended_at TEXT NOT NULL DEFAULT '',
    switch_model TEXT NOT NULL DEFAULT '',
    switch_effort TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE supervisor_input (
    id INTEGER PRIMARY KEY,
    body TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 16384),
    created_at TEXT NOT NULL,
    delivered_at TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE timer (
    id INTEGER PRIMARY KEY,
    due_at TEXT NOT NULL,
    every INTEGER NOT NULL DEFAULT 0 CHECK (every = 0 OR every >= 60),
    task_id INTEGER REFERENCES task(id),
    note TEXT NOT NULL DEFAULT '' CHECK (length(note) <= 200),
    status TEXT NOT NULL CHECK (status IN ('armed', 'fired', 'cancelled')),
    created_at TEXT NOT NULL,
    ended_at TEXT NOT NULL DEFAULT '',
    waits INTEGER NOT NULL DEFAULT 0 CHECK (waits IN (0, 1))
) STRICT;

CREATE INDEX timer_status ON timer(status, task_id);

CREATE TABLE device (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 40),
    login TEXT NOT NULL DEFAULT '' CHECK (length(login) <= 254),
    secret_hash TEXT NOT NULL CHECK (length(secret_hash) = 64),
    paired_at TEXT NOT NULL,
    phone TEXT NOT NULL DEFAULT '' CHECK (phone = '' OR length(phone) = 16)
) STRICT;

CREATE TABLE project_grant (
    id INTEGER PRIMARY KEY,
    project TEXT NOT NULL REFERENCES project(name),
    harness TEXT NOT NULL,
    pattern TEXT NOT NULL CHECK (pattern != ''),
    created_at TEXT NOT NULL,
    UNIQUE (project, harness, pattern)
) STRICT;
