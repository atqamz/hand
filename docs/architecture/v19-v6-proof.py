import gzip
import hashlib
import json
import re
import runpy
import sqlite3
import sys
from pathlib import Path


ROOT = Path(sys.argv[1]).resolve() if len(sys.argv) == 2 else Path(__file__).resolve().parent
EXPECTED_DDL_SHA256 = "ab04b86f04fffa18c1e058661813714bc3ffd70429b34746e533c2cbc20b31e6"
EXPECTED_SCHEMA_FINGERPRINT = "ccb9debabf5e194a5d290b6593d1616043bc433561074f09941711de685851ef"
EXPECTED_COUNTS = {"tables": 57, "indexes": 39, "triggers": 289, "sql_objects": 385, "sqlite_autoindexes": 33}
EXPECTED_V5_DDL_SHA256 = "e89280ddb3751142078d27e1f4203d45462cc5c9c03c04277948f090e4521a66"
EXPECTED_V5_PROOF_SHA256 = "4546b5796cc0badd102e249e2c329fbfc98cd09237a3b3b257b1844a73d2462d"


compressed = (ROOT / "v19-v6.sql.gz").read_bytes()
raw = gzip.decompress(compressed)
assert gzip.compress(raw, compresslevel=9, mtime=0) == compressed
assert hashlib.sha256(raw).hexdigest() == EXPECTED_DDL_SHA256
v5 = gzip.decompress((ROOT / "v19-v5.sql.gz").read_bytes()).decode()
assert hashlib.sha256(v5.encode()).hexdigest() == EXPECTED_V5_DDL_SHA256
tables = re.findall(r"CREATE TABLE (\w+) \((.*?)\) STRICT;", v5, re.S)
assert len(tables) == 57
for table, body in tables:
    if table in ("fleet", "external_operation_event"):
        continue
    v5 = v5.replace(f"CREATE TABLE {table} ({body}) STRICT;",
                    f"CREATE TABLE {table} ({body}) STRICT, WITHOUT ROWID;")
original = "CREATE TABLE external_operation_event (\n    id INTEGER PRIMARY KEY,"
assert v5.count(original) == 1
base = v5.replace(original, "CREATE TABLE external_operation_event (\n    id INTEGER PRIMARY KEY CHECK (id>0),")
overlay = (ROOT / "v19-v6-no-replace.sql").read_text()
assert raw.decode() == base.rstrip() + "\n\n" + overlay

namespace = {"__name__": "v19_v5_proof", "__file__": str(ROOT / "v19-proof-v5.py")}
source = gzip.decompress((ROOT / "v19-proof-v5.py.gz").read_bytes()).decode()
assert hashlib.sha256(source.encode()).hexdigest() == EXPECTED_V5_PROOF_SHA256
exec(compile(source, namespace["__file__"], "exec"), namespace)
ddl = raw.decode()
db = namespace["open_fresh"](ddl)
fingerprint = namespace["schema_fingerprint"](db)
counts = namespace["object_counts"](db)
assert fingerprint == EXPECTED_SCHEMA_FINGERPRINT
assert counts == EXPECTED_COUNTS
assert db.execute("PRAGMA foreign_key_check").fetchall() == []
assert db.execute("PRAGMA integrity_check").fetchone() == ("ok",)
no_delete = sorted(row[0] for row in db.execute("SELECT tbl_name FROM sqlite_schema WHERE type='trigger' AND name LIKE '%_no_delete'"))
assert no_delete == sorted(table for table, _ in tables)
rowid_tables = sorted(row[1] for row in db.execute("PRAGMA table_list") if row[1] in no_delete and row[4] == 0)
assert rowid_tables == ["external_operation_event", "fleet"]
generator = runpy.run_path(str(ROOT / "v19-v6-generate.py"))
assert overlay == "\n".join(generator["replacement_guard"](db, table, operation)
                           for table in no_delete for operation in ("INSERT", "UPDATE"))
guard_lookups = 0
for table in no_delete:
    for _, index, unique, _, partial in db.execute(f"PRAGMA index_list({table})"):
        if not unique:
            continue
        columns = [row[2] for row in db.execute(f"PRAGMA index_xinfo({index})") if row[5]]
        predicate = ""
        if partial:
            sql = db.execute("SELECT sql FROM sqlite_schema WHERE name=?", (index,)).fetchone()[0]
            predicate = " AND " + re.search(r"\bWHERE\s+(.+)$", sql).group(1)
        where = " AND ".join(f"{column}=?" for column in columns) + predicate
        plan = " ".join(row[3] for row in db.execute(f"EXPLAIN QUERY PLAN SELECT 1 FROM {table} WHERE {where}", ("x",) * len(columns)))
        assert "SEARCH" in plan and "SCAN" not in plan, (table, index, plan)
        guard_lookups += 1
assert guard_lookups == 94


def query_plan_proof_v6():
    f = namespace["build_runtime"](ddl)
    f.execute("INSERT INTO worker_report(id,attempt_id,executor_binding_id,source_prefix_digest,source_end_offset,report_state,note,created_at) VALUES('R1','A1','E1','p',1,'working','n',?)", (f.ts(),))
    f.execute("INSERT INTO task_hold(id,task_id,ordinal,kind,reason,evidence_digest,created_at) VALUES('H1','T1',1,'operator','r','d',?)", (f.ts(),))
    f.execute("INSERT INTO attempt_backoff(id,attempt_id,ordinal,reason,not_before,evidence_digest,created_at) VALUES('B1','A1',1,'rate-limit','later','d',?)", (f.ts(),))
    f.execute("BEGIN IMMEDIATE")
    f.execute("INSERT INTO repair_target(repair_id,executor_binding_id) VALUES('REP1','E1')")
    f.execute("INSERT INTO repair(id,repair_code,reason,evidence_digest,created_at) VALUES('REP1','code','r','d',?)", (f.ts(),))
    f.execute("COMMIT")
    f.op("O-WAKE-P", "worker-wake", "herdr", "executor-control", "E1")
    f.execute("INSERT INTO worker_wake_operation(operation_id,attempt_id,session_binding_id,executor_binding_id,pending_through_ordinal,wake_reason,doorbell_digest) VALUES('O-WAKE-P','A1','S1','E1',1,'wake','d')")
    f.claim("O-WAKE-P", "executor-control", "E1")
    checks = {
        "active_plan": ("SELECT id FROM plan WHERE task_id=? AND lifecycle='active'", ("T1",), "plan_one_active_by_task"),
        "active_attempt": ("SELECT id FROM attempt WHERE plan_id=? AND lifecycle='active'", ("PL1",), "attempt_one_active_by_plan"),
        "worker_input_order": ("SELECT id FROM worker_input WHERE executor_binding_id=? ORDER BY ordinal", ("E1",), "worker_input_current_order"),
        "unack_worker_input": ("SELECT wi.id FROM worker_input wi LEFT JOIN worker_input_acknowledgement a ON a.worker_input_id=wi.id WHERE wi.executor_binding_id=? AND a.worker_input_id IS NULL ORDER BY wi.ordinal", ("E1",), "worker_input_current_order"),
        "latest_worker_reports": ("SELECT id FROM worker_report WHERE attempt_id=? ORDER BY created_at DESC,id LIMIT 10", ("A1",), "worker_report_attempt_latest"),
        "worker_report_source_tail": ("SELECT id,source_prefix_digest,source_end_offset FROM worker_report WHERE attempt_id=? ORDER BY source_end_offset DESC,id LIMIT 1", ("A1",), "worker_report_attempt_source_order"),
        "open_holds": ("SELECT h.id FROM task_hold h LEFT JOIN task_hold_resolution r ON r.hold_id=h.id WHERE h.task_id=? AND r.hold_id IS NULL ORDER BY h.ordinal DESC", ("T1",), "task_hold_task_history"),
        "open_backoffs": ("SELECT b.id FROM attempt_backoff b LEFT JOIN attempt_backoff_resolution r ON r.backoff_id=b.id WHERE b.attempt_id=? AND r.backoff_id IS NULL ORDER BY b.ordinal DESC", ("A1",), "attempt_backoff_attempt_history"),
        "open_repairs_by_executor": ("SELECT r.id FROM repair_target rt JOIN repair r ON r.id=rt.repair_id LEFT JOIN repair_resolution rr ON rr.repair_id=r.id WHERE rt.executor_binding_id=? AND rr.repair_id IS NULL", ("E1",), "repair_target_executor"),
        "unresolved_operations": ("SELECT id FROM external_operation WHERE project_id=? AND kind='worker-wake' AND state IN ('prepared','submitted','uncertain') ORDER BY created_at", ("P1",), "external_operation_unresolved"),
        "scope_claim": ("SELECT operation_id FROM operation_scope_claim WHERE scope_kind=? AND scope_key=?", ("executor-control", "E1"), "operation_scope_claim_lookup"),
        "attempt_fallback_history": ("SELECT ordinal,rejection_reason FROM attempt_fallback_step WHERE attempt_id=? AND ordinal>? ORDER BY ordinal LIMIT 20", ("A1", 0), "USING PRIMARY KEY"),
        "task_archive_lookup": ("SELECT task_id FROM task_archive WHERE task_id=?", ("T1",), "USING PRIMARY KEY"),
    }
    out = {}
    for name, (sql, params, expected) in checks.items():
        plan = namespace["explain"](f.db, sql, params)
        assert expected in plan, (name, plan)
        out[name] = plan
    return out


results = {}
results["guard_lookup_plans"] = guard_lookups
for name in (
    "representative_flow", "adversarial_proof", "task_archive_proof",
    "worker_report_tail_proof", "routing_provenance_proof",
):
    results[name] = len(namespace[name](ddl))
results["query_plan_proof"] = len(query_plan_proof_v6())
results["cutover_target_proof"] = namespace["cutover_target_proof"](ddl, EXPECTED_DDL_SHA256, fingerprint)
runpy.run_path(str(ROOT / "v19-v6-no-replace-proof.py"))
print(json.dumps({"ddl_sha256": EXPECTED_DDL_SHA256, "schema_fingerprint": fingerprint,
                  "object_counts": counts, "sqlite_version": sqlite3.sqlite_version,
                  "results": results, "status": "PASS"}, sort_keys=True))
