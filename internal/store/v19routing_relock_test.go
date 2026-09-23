package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

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
