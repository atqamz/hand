CREATE TABLE project (
    name TEXT PRIMARY KEY,
    repo TEXT NOT NULL,
    created_at TEXT NOT NULL
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
