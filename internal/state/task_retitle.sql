ALTER TABLE task ADD COLUMN former_titles TEXT NOT NULL DEFAULT '';

DROP TRIGGER task_fts_insert;

DROP TRIGGER task_fts_delete;

DROP TRIGGER task_fts_update;

DROP VIEW history_task;

CREATE VIEW history_task AS SELECT id, id AS task_id, created_at, rtrim(title || char(10) || former_titles, char(10)) AS body FROM task;

INSERT INTO task_fts(task_fts) VALUES ('rebuild');

CREATE TRIGGER task_fts_insert AFTER INSERT ON task BEGIN
    INSERT INTO task_fts(rowid, body) VALUES (new.id, rtrim(new.title || char(10) || new.former_titles, char(10)));
END;

CREATE TRIGGER task_fts_delete AFTER DELETE ON task BEGIN
    INSERT INTO task_fts(task_fts, rowid, body) VALUES ('delete', old.id, rtrim(old.title || char(10) || old.former_titles, char(10)));
END;

CREATE TRIGGER task_fts_update AFTER UPDATE OF title, former_titles ON task BEGIN
    INSERT INTO task_fts(task_fts, rowid, body) VALUES ('delete', old.id, rtrim(old.title || char(10) || old.former_titles, char(10)));
    INSERT INTO task_fts(rowid, body) VALUES (new.id, rtrim(new.title || char(10) || new.former_titles, char(10)));
END;
