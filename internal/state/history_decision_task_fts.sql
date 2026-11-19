CREATE VIEW history_decision AS SELECT id, task_id, created_at, rtrim(question || char(10) || answer, char(10)) AS body FROM decision;

CREATE VIEW history_task AS SELECT id, id AS task_id, created_at, title AS body FROM task;

CREATE VIRTUAL TABLE decision_fts USING fts5(body, content='history_decision', content_rowid='id', tokenize='unicode61 remove_diacritics 2');

CREATE VIRTUAL TABLE task_fts USING fts5(body, content='history_task', content_rowid='id', tokenize='unicode61 remove_diacritics 2');

INSERT INTO decision_fts(decision_fts) VALUES ('rebuild');

INSERT INTO task_fts(task_fts) VALUES ('rebuild');

CREATE TRIGGER decision_fts_insert AFTER INSERT ON decision BEGIN
    INSERT INTO decision_fts(rowid, body) VALUES (new.id, rtrim(new.question || char(10) || new.answer, char(10)));
END;

CREATE TRIGGER decision_fts_delete AFTER DELETE ON decision BEGIN
    INSERT INTO decision_fts(decision_fts, rowid, body) VALUES ('delete', old.id, rtrim(old.question || char(10) || old.answer, char(10)));
END;

CREATE TRIGGER decision_fts_update AFTER UPDATE OF question, answer ON decision BEGIN
    INSERT INTO decision_fts(decision_fts, rowid, body) VALUES ('delete', old.id, rtrim(old.question || char(10) || old.answer, char(10)));
    INSERT INTO decision_fts(rowid, body) VALUES (new.id, rtrim(new.question || char(10) || new.answer, char(10)));
END;

CREATE TRIGGER task_fts_insert AFTER INSERT ON task BEGIN
    INSERT INTO task_fts(rowid, body) VALUES (new.id, new.title);
END;

CREATE TRIGGER task_fts_delete AFTER DELETE ON task BEGIN
    INSERT INTO task_fts(task_fts, rowid, body) VALUES ('delete', old.id, old.title);
END;

CREATE TRIGGER task_fts_update AFTER UPDATE OF title ON task BEGIN
    INSERT INTO task_fts(task_fts, rowid, body) VALUES ('delete', old.id, old.title);
    INSERT INTO task_fts(rowid, body) VALUES (new.id, new.title);
END;
