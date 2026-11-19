CREATE TABLE supervisor_reply (
    id INTEGER PRIMARY KEY,
    supervisor_id INTEGER NOT NULL REFERENCES supervisor(id),
    session TEXT NOT NULL,
    at TEXT NOT NULL,
    n INTEGER NOT NULL CHECK (n >= 0),
    body TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 32768),
    UNIQUE (session, at, n)
) STRICT;

CREATE INDEX supervisor_reply_sup ON supervisor_reply(supervisor_id, id);
CREATE INDEX supervisor_reply_at ON supervisor_reply(at);
