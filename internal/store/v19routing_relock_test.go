package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestCanonicalV19RoutingRelockRejectsNULHiddenOverflow(t *testing.T) {
	db := candidateV19RoutingRelockDB(t, "docs/architecture/v19-v5.sql.gz")
	for _, field := range []string{"profile_override", "harness_override", "model_override", "effort_override"} {
		t.Run(field, func(t *testing.T) {
			query := fmt.Sprintf(`INSERT INTO attempt(id,plan_id,ordinal,worker_harness_ref,session_adapter_ref,created_at,%s)
				VALUES('attempt-nul','plan-root',1,'harness','herdr','2026-09-24T00:00:00Z',?)`, field)
			if _, err := db.Exec(query, strings.Repeat("x", 512)+"\x00tail"); err == nil {
				t.Fatal("NUL-hidden oversized override accepted")
			}
		})
	}
	if _, err := db.Exec(`INSERT INTO attempt(id,plan_id,ordinal,worker_harness_ref,session_adapter_ref,created_at)
		VALUES('attempt-nul','plan-root',1,'harness','herdr','2026-09-24T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	query := `INSERT INTO attempt_fallback_step(attempt_id,ordinal,rejected_profile_ref,rejected_harness_ref,
		rejected_model_ref,rejected_effort_ref,rejection_reason,observation_digest,observed_at)
		VALUES('attempt-nul',1,?,?,?,?, 'quota-exhausted',?, '2026-09-24T00:00:00Z')`
	for _, field := range []string{"rejected_profile_ref", "rejected_harness_ref", "rejected_model_ref", "rejected_effort_ref", "observation_digest"} {
		t.Run(field, func(t *testing.T) {
			value := strings.Repeat("x", 512) + "\x00tail"
			if field == "observation_digest" {
				value = strings.Repeat("a", 64) + "\x00tail"
			}
			values := map[string]string{
				"rejected_profile_ref": "profile", "rejected_harness_ref": "harness",
				"rejected_model_ref": "model", "rejected_effort_ref": "effort",
				"observation_digest": strings.Repeat("a", 64),
			}
			values[field] = value
			if _, err := db.Exec(query, values["rejected_profile_ref"], values["rejected_harness_ref"],
				values["rejected_model_ref"], values["rejected_effort_ref"], values["observation_digest"]); err == nil {
				t.Fatal("NUL-hidden fallback value accepted")
			}
		})
	}
	if _, err := db.Exec(query, strings.Repeat("é", 512), "harness", "model", "effort", strings.Repeat("a", 64)); err != nil {
		t.Fatalf("valid 512-character fallback rejected: %v", err)
	}
}

func candidateV19RoutingRelockDB(t *testing.T, artifact string) *sql.DB {
	t.Helper()
	compressed := readV19ManifestArtifact(t, artifact)
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	ddl, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := open(Path(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(string(ddl)); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO fleet(singleton,fleet_id,created_at) VALUES(1,'fleet-1','2026-09-24T00:00:00Z')`,
		`INSERT INTO project(id,fleet_id,ordinal,display_name,created_at) VALUES('project-1','fleet-1',1,'project','2026-09-24T00:00:00Z')`,
		`INSERT INTO workspace_binding(id,project_id,ordinal,repository_locator,repository_identity_digest,common_git_dir,physical_identity_digest,revision,established_at)
		 VALUES('workspace-1','project-1',1,'repo','repo-digest','repo/.git','physical-digest','revision','2026-09-24T00:00:00Z')`,
		`INSERT INTO policy_revision(id,project_id,ordinal,policy_digest,created_at) VALUES('policy-1','project-1',1,'policy-digest','2026-09-24T00:00:00Z')`,
		`INSERT INTO task(id,project_id,ordinal,goal,goal_digest,created_at) VALUES('task-1','project-1',1,'goal','goal-digest','2026-09-24T00:00:00Z')`,
		`INSERT INTO plan(id,task_id,ordinal,lineage_kind,intent,judgment,basis,brief,brief_digest,workspace_binding_id,policy_revision_id,created_at)
		 VALUES('plan-root','task-1',1,'root','execute','bounded','basis','brief','brief-digest','workspace-1','policy-1','2026-09-24T00:00:00Z')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestCanonicalV19RelockRejectsReplaceWithRecursiveTriggersOff(t *testing.T) {
	db := candidateV19RoutingRelockDB(t, "docs/architecture/v19-v6.sql.gz")
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "PRAGMA recursive_triggers=OFF"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO project(id,fleet_id,ordinal,display_name,created_at)
		 VALUES('project-2','fleet-1',2,'second','2026-09-24T00:00:00Z')`,
		`INSERT INTO decision(id,task_id,scope_kind,question,created_at) VALUES('decision-1','task-1','task','original','2026-09-24T00:00:00Z')`,
		`INSERT INTO decision(id,task_id,scope_kind,question,created_at) VALUES('decision-2','task-1','task','second','2026-09-24T00:00:00Z')`,
		`INSERT INTO decision_answer(id,decision_id,answer,answer_digest,actor_kind,actor_ref,answered_at)
		 VALUES('answer-1','decision-1','original','digest','operator','operator','2026-09-24T00:01:00Z')`,
		`INSERT INTO decision_closure(decision_id,reason,closed_at,evidence_digest)
		 VALUES('decision-2','stale','2026-09-24T00:01:00Z','digest')`,
	} {
		if _, err := conn.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		`UPDATE OR REPLACE project SET display_name='second' WHERE id='project-1'`,
		`INSERT OR REPLACE INTO project(rowid,id,fleet_id,ordinal,display_name,created_at)
		 VALUES(1,'project-2','fleet-1',2,'other','2026-09-24T00:02:00Z')`,
		`INSERT OR REPLACE INTO project(id,fleet_id,ordinal,display_name,created_at)
		 VALUES('project-2','fleet-1',2,'project','2026-09-24T00:02:00Z')`,
		`INSERT OR REPLACE INTO decision(id,task_id,scope_kind,question,created_at)
		 VALUES('decision-1','task-1','task','replacement','2026-09-24T00:02:00Z')`,
		`INSERT OR REPLACE INTO decision_answer(id,decision_id,answer,answer_digest,actor_kind,actor_ref,answered_at)
		 VALUES('answer-1','decision-1','replacement','digest','operator','operator','2026-09-24T00:02:00Z')`,
		`INSERT OR REPLACE INTO decision_closure(decision_id,reason,closed_at,evidence_digest)
		 VALUES('decision-2','cancelled','2026-09-24T00:02:00Z','digest')`,
		`INSERT OR REPLACE INTO fleet(singleton,fleet_id,created_at)
		 VALUES(1,'fleet-1','2026-09-24T00:02:00Z')`,
	} {
		if _, err := conn.ExecContext(ctx, query); err == nil {
			t.Fatalf("immutable replacement accepted: %s", query)
		}
	}
}

// #323/#344: exact request/final provenance and fallback history must survive
// restart and rollback on the pinned Go driver, without authorizing execution.
func TestCanonicalV19RoutingRelockProvenance(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	db, err := openCanonicalV19Writer(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	insert := `INSERT INTO attempt(id,plan_id,ordinal,worker_harness_ref,worker_profile_ref,model_ref,effort_ref,
		session_adapter_ref,created_at,profile_override,harness_override,model_override,effort_override)
		VALUES('attempt-routing','plan-root',1,'selected-harness','selected-profile','selected-model','high',
		'herdr','2026-09-23T00:00:00Z','requested-profile','requested-harness','requested-model','low')`
	step := `INSERT INTO attempt_fallback_step(attempt_id,ordinal,rejected_profile_ref,rejected_harness_ref,
		rejected_model_ref,rejected_effort_ref,rejection_reason,observation_digest,observed_at)
		VALUES('attempt-routing',?,'requested-profile','requested-harness','requested-model','low',?,?,'2026-09-22T23:59:59Z')`
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(insert); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(step, 1, "quota-exhausted", strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(step, 2, "unknown", strings.Repeat("b", 64)); err == nil {
		t.Fatal("unknown capability became fallback authority")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM attempt)+(SELECT COUNT(*) FROM attempt_fallback_step)`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial Attempt after chain failure: %d %v", count, err)
	}
	tx, err = db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(insert); err != nil {
		t.Fatal(err)
	}
	reasons := []string{"harness-unavailable", "model-unavailable", "quota-exhausted", "capability-unavailable", "combination-unsupported"}
	for i, reason := range reasons {
		if _, err := tx.Exec(step, i+1, reason, strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = openCanonicalV19Writer(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	var requested, selected string
	if err := db.QueryRow(`SELECT model_override,model_ref FROM attempt WHERE id='attempt-routing'`).Scan(&requested, &selected); err != nil || requested != "requested-model" || selected != "selected-model" {
		t.Fatalf("request/final meaning lost after reopen: %q %q %v", requested, selected, err)
	}
	for _, query := range []string{
		`UPDATE attempt SET model_override=NULL WHERE id='attempt-routing'`,
		`UPDATE attempt_fallback_step SET rejection_reason='unknown'`,
		`DELETE FROM attempt_fallback_step`,
	} {
		if _, err := db.Exec(query); err == nil {
			t.Fatalf("history mutation accepted: %s", query)
		}
	}
	for _, ordinal := range []int{1, 7} {
		if _, err := db.Exec(step, ordinal, "quota-exhausted", strings.Repeat("a", 64)); err == nil {
			t.Fatalf("accepted duplicate/gapped ordinal %d", ordinal)
		}
	}
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT ordinal,rejection_reason FROM attempt_fallback_step
		WHERE attempt_id=? AND ordinal>? ORDER BY ordinal LIMIT 20`, "attempt-routing", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plans strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plans.WriteString(detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plans.String(), "sqlite_autoindex_attempt_fallback_step_1") || strings.Contains(plans.String(), "SCAN") {
		t.Fatalf("fallback history requires bounded indexed lookup: %s", plans.String())
	}
	_ = rows.Close()
	if err := validateCanonicalV19Schema(db); err != nil {
		t.Fatal(err)
	}
}

// Existing default-only Attempt creation remains valid; absent overrides do not
// become invented explicit requests or fallback observations.
func TestCanonicalV19RoutingRelockPreservesAbsentRequest(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptWriterInput("attempt-default", "plan-root")); err != nil {
		t.Fatal(err)
	}
	db, err := openCanonicalV19Writer(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var values [4]sql.NullString
	if err := db.QueryRow(`SELECT profile_override,harness_override,model_override,effort_override FROM attempt`).Scan(&values[0], &values[1], &values[2], &values[3]); err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if value.Valid {
			t.Fatal("absent override was fabricated")
		}
	}
	if _, err := db.Exec(`UPDATE attempt SET model_override=''`); err == nil {
		t.Fatal("absent override changed into explicit empty override")
	}
}
