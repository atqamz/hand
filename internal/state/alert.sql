CREATE TABLE alert (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    at TEXT NOT NULL,
    kind TEXT NOT NULL,
    ref TEXT NOT NULL,
    task_id INTEGER,
    text TEXT NOT NULL,
    link TEXT NOT NULL,
    fetched_at TEXT NOT NULL DEFAULT '',
    acked_at TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX alert_at ON alert(at);
