import gzip
import hashlib
import re
import sqlite3
from pathlib import Path


ROOT = Path(__file__).parent
SOURCE = ROOT / "v19-v5.sql.gz"
OVERLAY = ROOT / "v19-v6-no-replace.sql"
OUTPUT = ROOT / "v19-v6.sql.gz"
PROOF_SOURCE = ROOT / "v19-v6-proof.py"
PROOF_OUTPUT = ROOT / "v19-proof-v6.py.gz"
SOURCE_SHA256 = "e89280ddb3751142078d27e1f4203d45462cc5c9c03c04277948f090e4521a66"


def replacement_guard(db, table, operation):
    collisions = []
    indexes = db.execute(f"PRAGMA index_list({table})").fetchall()
    primary = [row for row in db.execute(f"PRAGMA table_info({table})") if row[5]]
    if not primary:
        raise ValueError(f"no primary key for {table}")
    old = ""
    if operation == "UPDATE":
        identity = " AND ".join(f"{row[1]}=OLD.{row[1]}" for row in primary)
        old = f" AND NOT ({identity})"
    for _, name, unique, _, partial in indexes:
        if not unique:
            continue
        key = [row for row in db.execute(f"PRAGMA index_xinfo({name})") if row[5]]
        if not key or any(row[2] is None or row[4] != "BINARY" for row in key):
            raise ValueError(f"unsupported unique index {name}")
        match = " AND ".join(f"{row[2]}=NEW.{row[2]}" for row in key) + old
        if partial:
            sql = db.execute("SELECT sql FROM sqlite_schema WHERE name=?", (name,)).fetchone()[0]
            predicate = re.search(r"\bWHERE\s+([a-z_]+)\s*=\s*'([^']*)'\s*$", sql)
            if predicate is None:
                raise ValueError(f"unsupported partial unique index {name}")
            field, value = predicate.groups()
            match += f" AND {field}='{value}'"
            collisions.append(f"(NEW.{field}='{value}' AND EXISTS (SELECT 1 FROM {table} WHERE {match}))")
        else:
            collisions.append(f"EXISTS (SELECT 1 FROM {table} WHERE {match})")
    if not any(row[3] == "pk" for row in indexes):
        if len(primary) != 1 or primary[0][2].upper() != "INTEGER":
            raise ValueError(f"unsupported rowid primary key {table}")
        field = primary[0][1]
        collisions.append(f"EXISTS (SELECT 1 FROM {table} WHERE {field}=NEW.{field}{old})")
    if not collisions:
        raise ValueError(f"no replacement key for {table}")
    condition = "\n  OR ".join(collisions)
    suffix = "" if operation == "INSERT" else "_update"
    return (f"CREATE TRIGGER {table}_no_replace{suffix}\n"
            f"BEFORE {operation} ON {table}\n"
            f"WHEN {condition}\n"
            f"BEGIN\n"
            f"  SELECT RAISE(ABORT,'{table} is immutable');\n"
            f"END;\n")


def main():
    ddl = gzip.decompress(SOURCE.read_bytes()).decode()
    if hashlib.sha256(ddl.encode()).hexdigest() != SOURCE_SHA256:
        raise ValueError("revision-5 DDL changed")
    tables = re.findall(r"CREATE TABLE (\w+) \((.*?)\) STRICT;", ddl, re.S)
    if len(tables) != 57:
        raise ValueError("immutable table set changed")
    for table, body in tables:
        if table in ("fleet", "external_operation_event"):
            continue
        original_table = f"CREATE TABLE {table} ({body}) STRICT;"
        ddl = ddl.replace(original_table, f"CREATE TABLE {table} ({body}) STRICT, WITHOUT ROWID;")
    original = "CREATE TABLE external_operation_event (\n    id INTEGER PRIMARY KEY,"
    if ddl.count(original) != 1:
        raise ValueError("external operation event identity changed")
    ddl = ddl.replace(original, "CREATE TABLE external_operation_event (\n    id INTEGER PRIMARY KEY CHECK (id>0),")
    tables = sorted(set(re.findall(r"CREATE TRIGGER \w+_no_delete\s+BEFORE DELETE ON (\w+)", ddl)))
    db = sqlite3.connect(":memory:")
    db.executescript(ddl)
    guards = "\n".join(replacement_guard(db, table, operation)
                       for table in tables for operation in ("INSERT", "UPDATE"))
    db.executescript(guards)
    OVERLAY.write_text(guards)
    OUTPUT.write_bytes(gzip.compress((ddl.rstrip() + "\n\n" + guards).encode(), compresslevel=9, mtime=0))
    PROOF_OUTPUT.write_bytes(gzip.compress(PROOF_SOURCE.read_bytes(), compresslevel=9, mtime=0))
    print(f"generated {len(tables) * 2} replacement guards")


if __name__ == "__main__":
    main()
