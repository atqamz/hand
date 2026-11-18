CREATE VIRTUAL TABLE supervisor_input_fts USING fts5(body, content='supervisor_input', content_rowid='id', tokenize='unicode61 remove_diacritics 2');

CREATE VIRTUAL TABLE report_fts USING fts5(body, content='report', content_rowid='id', tokenize='unicode61 remove_diacritics 2');

CREATE VIRTUAL TABLE supervisor_reply_fts USING fts5(body, content='supervisor_reply', content_rowid='id', tokenize='unicode61 remove_diacritics 2');

INSERT INTO supervisor_input_fts(supervisor_input_fts) VALUES ('rebuild');

INSERT INTO report_fts(report_fts) VALUES ('rebuild');

INSERT INTO supervisor_reply_fts(supervisor_reply_fts) VALUES ('rebuild');

CREATE TRIGGER supervisor_input_fts_insert AFTER INSERT ON supervisor_input BEGIN
    INSERT INTO supervisor_input_fts(rowid, body) VALUES (new.id, new.body);
END;

CREATE TRIGGER supervisor_input_fts_delete AFTER DELETE ON supervisor_input BEGIN
    INSERT INTO supervisor_input_fts(supervisor_input_fts, rowid, body) VALUES ('delete', old.id, old.body);
END;

CREATE TRIGGER supervisor_input_fts_update AFTER UPDATE OF body ON supervisor_input BEGIN
    INSERT INTO supervisor_input_fts(supervisor_input_fts, rowid, body) VALUES ('delete', old.id, old.body);
    INSERT INTO supervisor_input_fts(rowid, body) VALUES (new.id, new.body);
END;

CREATE TRIGGER report_fts_insert AFTER INSERT ON report BEGIN
    INSERT INTO report_fts(rowid, body) VALUES (new.id, new.body);
END;

CREATE TRIGGER report_fts_delete AFTER DELETE ON report BEGIN
    INSERT INTO report_fts(report_fts, rowid, body) VALUES ('delete', old.id, old.body);
END;

CREATE TRIGGER report_fts_update AFTER UPDATE OF body ON report BEGIN
    INSERT INTO report_fts(report_fts, rowid, body) VALUES ('delete', old.id, old.body);
    INSERT INTO report_fts(rowid, body) VALUES (new.id, new.body);
END;

CREATE TRIGGER supervisor_reply_fts_insert AFTER INSERT ON supervisor_reply BEGIN
    INSERT INTO supervisor_reply_fts(rowid, body) VALUES (new.id, new.body);
END;

CREATE TRIGGER supervisor_reply_fts_delete AFTER DELETE ON supervisor_reply BEGIN
    INSERT INTO supervisor_reply_fts(supervisor_reply_fts, rowid, body) VALUES ('delete', old.id, old.body);
END;

CREATE TRIGGER supervisor_reply_fts_update AFTER UPDATE OF body ON supervisor_reply BEGIN
    INSERT INTO supervisor_reply_fts(supervisor_reply_fts, rowid, body) VALUES ('delete', old.id, old.body);
    INSERT INTO supervisor_reply_fts(rowid, body) VALUES (new.id, new.body);
END;
