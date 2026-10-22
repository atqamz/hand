CREATE TABLE device (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 40),
    login TEXT NOT NULL DEFAULT '' CHECK (length(login) <= 254),
    secret_hash TEXT NOT NULL CHECK (length(secret_hash) = 64),
    paired_at TEXT NOT NULL
) STRICT;
