import gzip
import sqlite3
from pathlib import Path


ROOT = Path(__file__).parent
DDL = gzip.decompress((ROOT / "v19-v6.sql.gz").read_bytes()).decode()


def bare_database(recursive):
    db = sqlite3.connect(":memory:", isolation_level=None)
    db.execute("PRAGMA foreign_keys=ON")
    db.executescript(DDL)
    db.execute(f"PRAGMA recursive_triggers={recursive}")
    return db


def database(recursive):
    db = bare_database(recursive)
    db.execute("INSERT INTO fleet(singleton,fleet_id,created_at) VALUES(1,'fleet-1','t0')")
    db.execute("INSERT INTO project(id,fleet_id,ordinal,display_name,created_at) VALUES('p','fleet-1',1,'project','t0')")
    db.execute("INSERT INTO task(id,project_id,ordinal,goal,goal_digest,created_at) VALUES('t','p',1,'goal','digest','t0')")
    db.execute("INSERT INTO decision(id,task_id,scope_kind,question,created_at) VALUES('d1','t','task','original','t1')")
    db.execute("INSERT INTO decision(id,task_id,scope_kind,question,created_at) VALUES('d2','t','task','second','t1')")
    return db


def reject(db, statement):
    try:
        db.execute(statement)
    except sqlite3.IntegrityError:
        return
    raise AssertionError(f"immutable row replaced: {statement}")


def verify(db):
    assert db.execute("PRAGMA foreign_key_check").fetchall() == []
    assert db.execute("PRAGMA integrity_check").fetchone() == ("ok",)


for recursive in (0, 1):
    db = bare_database(recursive)
    db.execute("INSERT INTO fleet(singleton,fleet_id,created_at) VALUES(1,'fleet-1','t0')")
    reject(db, "INSERT OR REPLACE INTO fleet(singleton,fleet_id,created_at) VALUES(1,'fleet-1','t1')")
    assert db.execute("SELECT created_at FROM fleet").fetchone() == ("t0",)
    db.execute("INSERT INTO project(id,fleet_id,ordinal,display_name,created_at) VALUES('p','fleet-1',1,'project','t0')")
    reject(db, "INSERT OR REPLACE INTO project(id,fleet_id,ordinal,display_name,created_at) VALUES('p2','fleet-1',2,'project','t1')")
    for field in ("rowid", "_rowid_", "oid"):
        try:
            db.execute(f"INSERT OR REPLACE INTO project({field},id,fleet_id,ordinal,display_name,created_at) VALUES(1,'p2','fleet-1',2,'other','t1')")
        except sqlite3.OperationalError:
            pass
        else:
            raise AssertionError(f"hidden row identity admitted: {field}")
    assert db.execute("SELECT id FROM project").fetchone() == ("p",)
    verify(db)

    db = bare_database(recursive)
    db.execute("INSERT INTO fleet(singleton,fleet_id,created_at) VALUES(1,'fleet-1','t0')")
    db.execute("INSERT INTO project(id,fleet_id,ordinal,display_name,created_at) VALUES('p1','fleet-1',1,'first','t0')")
    db.execute("INSERT INTO project(id,fleet_id,ordinal,display_name,created_at) VALUES('p2','fleet-1',2,'second','t0')")
    reject(db, "UPDATE OR REPLACE project SET display_name='second' WHERE id='p1'")
    assert db.execute("SELECT id,display_name FROM project ORDER BY id").fetchall() == [("p1", "first"), ("p2", "second")]
    verify(db)

    db = database(recursive)
    reject(db, "INSERT OR REPLACE INTO decision(id,task_id,scope_kind,question,created_at) VALUES('d1','t','task','replacement','t2')")
    assert db.execute("SELECT question FROM decision WHERE id='d1'").fetchone() == ("original",)
    verify(db)

    db = database(recursive)
    db.execute("INSERT INTO decision_answer(id,decision_id,answer,answer_digest,actor_kind,actor_ref,answered_at) VALUES('a1','d1','original','digest','operator','operator','t1')")
    reject(db, "INSERT OR REPLACE INTO decision_answer(id,decision_id,answer,answer_digest,actor_kind,actor_ref,answered_at) VALUES('a1','d2','replacement','digest','operator','operator','t2')")
    reject(db, "INSERT OR REPLACE INTO decision_answer(id,decision_id,answer,answer_digest,actor_kind,actor_ref,answered_at) VALUES('a2','d1','replacement','digest','operator','operator','t2')")
    assert db.execute("SELECT id,decision_id,answer FROM decision_answer").fetchall() == [("a1", "d1", "original")]
    verify(db)

    db = database(recursive)
    db.execute("INSERT INTO decision_closure(decision_id,reason,closed_at,evidence_digest) VALUES('d1','stale','t1','digest')")
    reject(db, "INSERT OR REPLACE INTO decision_closure(decision_id,reason,closed_at,evidence_digest) VALUES('d1','cancelled','t2','digest')")
    assert db.execute("SELECT reason FROM decision_closure WHERE decision_id='d1'").fetchone() == ("stale",)
    verify(db)

    db = bare_database(recursive)
    db.execute("INSERT INTO fleet(singleton,fleet_id,created_at) VALUES(1,'fleet-1','t0')")
    db.execute("INSERT INTO project(id,fleet_id,ordinal,display_name,created_at) VALUES('p','fleet-1',1,'project','t0')")
    db.execute("INSERT INTO workspace_binding(id,project_id,ordinal,repository_locator,repository_identity_digest,common_git_dir,physical_identity_digest,revision,established_at) VALUES('w','p',1,'repo','digest','repo/.git','physical','revision','t0')")
    db.execute("INSERT INTO policy_revision(id,project_id,ordinal,policy_digest,created_at) VALUES('policy','p',1,'digest','t0')")
    db.execute("INSERT INTO task(id,project_id,ordinal,goal,goal_digest,created_at) VALUES('t','p',1,'goal','digest','t0')")
    db.execute("INSERT INTO plan(id,task_id,ordinal,lineage_kind,intent,judgment,basis,brief,brief_digest,workspace_binding_id,policy_revision_id,created_at) VALUES('plan','t',1,'root','explore','bounded','basis','brief','digest','w','policy','t0')")
    db.execute("INSERT INTO attempt(id,plan_id,ordinal,worker_harness_ref,session_adapter_ref,created_at) VALUES('a1','plan',1,'harness','herdr','t0')")
    reject(db, "INSERT OR REPLACE INTO attempt(id,plan_id,ordinal,worker_harness_ref,session_adapter_ref,created_at) VALUES('a2','plan',2,'harness','herdr','t1')")
    assert db.execute("SELECT id FROM attempt").fetchone() == ("a1",)
    db.execute("UPDATE attempt SET lifecycle='failed',terminal_at='t2' WHERE id='a1'")
    db.execute("INSERT INTO attempt(id,plan_id,ordinal,worker_harness_ref,session_adapter_ref,created_at) VALUES('a2','plan',2,'harness','herdr','t3')")
    db.execute("INSERT INTO external_operation(id,kind,adapter_ref,operation_key,request_digest,project_id,primary_scope_kind,primary_scope_key,created_at,state_changed_at) VALUES('op','qualification','adapter','key','digest','p','qualification','scope','t0','t0')")
    reject(db, "INSERT OR REPLACE INTO external_operation_event(id,operation_id,from_state,to_state,observed_at,evidence_digest) VALUES(1,'op','','submitted','t1','digest')")
    reject(db, "INSERT INTO external_operation_event(id,operation_id,from_state,to_state,observed_at,evidence_digest) VALUES(-1,'op','','prepared','t1','digest')")
    db.execute("INSERT INTO external_operation_event(operation_id,from_state,to_state,observed_at,evidence_digest) VALUES('op','','prepared','t1','digest')")
    assert db.execute("SELECT id FROM external_operation_event ORDER BY id").fetchall() == [(1,), (2,)]
    verify(db)

if __name__ == "__main__":
    print("PASS immutable REPLACE guards and valid Attempt retry")
