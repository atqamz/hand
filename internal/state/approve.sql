ALTER TABLE attempt ADD COLUMN approve TEXT NOT NULL DEFAULT 'never' CHECK (approve IN ('never', 'ask'));

CREATE TABLE project_grant (
    id INTEGER PRIMARY KEY,
    project TEXT NOT NULL REFERENCES project(name),
    harness TEXT NOT NULL,
    pattern TEXT NOT NULL CHECK (pattern != ''),
    created_at TEXT NOT NULL,
    UNIQUE (project, harness, pattern)
) STRICT;
