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
