package store

import (
	"context"
	"strings"
	"testing"
)

func TestCanonicalV19WorkerReportTailRejectsCheckpointRollbackFork(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	attempt := canonicalV19AttemptWriterInput("attempt-worker-report-tail", "plan-root")
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, attempt); err != nil {
		t.Fatal(err)
	}

	db, err := open(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	for _, report := range []struct {
		id, digest string
		offset     int
	}{
		{id: "R1", digest: "prefix-r1", offset: 10},
		{id: "R2", digest: "prefix-r2", offset: 20},
	} {
		if _, err := db.Exec(`INSERT INTO worker_report(
			id,attempt_id,source_prefix_digest,source_end_offset,report_state,note,created_at
		) VALUES(?,?,?,?,?,?,?)`, report.id, attempt.ID, report.digest, report.offset,
			"working", report.id, "2026-09-16T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}

	// A restored R1 checkpoint cannot authorize a fork after canonical R2.
	result, err := db.Exec(`INSERT INTO worker_report(
		id,attempt_id,source_prefix_digest,source_end_offset,report_state,note,created_at
	)
	SELECT ?,?,?,?,?,?,?
	WHERE EXISTS (
		SELECT 1
		FROM (
			SELECT id,source_prefix_digest,source_end_offset
			FROM worker_report
			WHERE attempt_id=?
			ORDER BY source_end_offset DESC,id
			LIMIT 1
		) AS tail
		WHERE tail.id=? AND tail.source_prefix_digest=? AND tail.source_end_offset=?
	)`, "R-FORK", attempt.ID, "prefix-fork", 30, "failed", "fork", "2026-09-16T00:01:00Z",
		attempt.ID, "R1", "prefix-r1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := result.RowsAffected(); err != nil {
		t.Fatal(err)
	} else if rows != 0 {
		t.Fatalf("rollback-fork rows inserted = %d, want 0", rows)
	}

	rows, err := db.Query(`EXPLAIN QUERY PLAN
		SELECT id,source_prefix_digest,source_end_offset
		FROM worker_report
		WHERE attempt_id=?
		ORDER BY source_end_offset DESC,id
		LIMIT 1`, attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if got := plan.String(); !strings.Contains(got, "worker_report_attempt_source_order") || strings.Contains(got, "TEMP B-TREE") {
		t.Fatalf("canonical tail query is not bounded by worker_report_attempt_source_order:\n%s", got)
	}
}
