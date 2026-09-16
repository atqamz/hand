package store

import (
	"context"
	"strings"
	"testing"
)

func TestCanonicalV19WorkerReportTailRejectsCheckpointRollbackFork(t *testing.T) {
	type sourceBoundary struct {
		id, digest string
		offset     int
	}

	ctx := context.Background()
	fixture := canonicalV19AttemptWriterFixture(t)
	attempt := canonicalV19AttemptWriterInput("attempt-worker-report-tail", "plan-root")
	if _, err := CreateCanonicalV19Attempt(ctx, fixture.Home, attempt); err != nil {
		t.Fatal(err)
	}

	db, err := open(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	for _, report := range []sourceBoundary{
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

	checkpoint := sourceBoundary{id: "R1", digest: "prefix-r1", offset: 10}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	// Reproduce PR #581's checkpoint-only failure: restored R1 is syntactically
	// valid and still exists, so it authorizes a distinct-offset fork after R2.
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	var predecessor sourceBoundary
	if err := conn.QueryRowContext(ctx, `SELECT id,source_prefix_digest,source_end_offset
		FROM worker_report WHERE id=?`, checkpoint.id).Scan(
		&predecessor.id, &predecessor.digest, &predecessor.offset,
	); err != nil {
		t.Fatal(err)
	}
	if predecessor != checkpoint {
		t.Fatalf("restored checkpoint predecessor = %#v, want %#v", predecessor, checkpoint)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO worker_report(
		id,attempt_id,source_prefix_digest,source_end_offset,report_state,note,created_at
	) VALUES(?,?,?,?,?,?,?)`, "R-FORK", attempt.ID, "prefix-fork", 30,
		"failed", "fork", "2026-09-16T00:01:00Z"); err != nil {
		t.Fatal(err)
	}
	var checkpointOnlyCount int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM worker_report WHERE attempt_id=?`, attempt.ID).
		Scan(&checkpointOnlyCount); err != nil {
		t.Fatal(err)
	}
	if checkpointOnlyCount != 3 {
		t.Fatalf("checkpoint-only WorkerReport rows = %d, want reproduced fork count 3", checkpointOnlyCount)
	}
	if _, err := conn.ExecContext(ctx, `ROLLBACK`); err != nil {
		t.Fatal(err)
	}

	// The relocked writer compares the same checkpoint tuple with the exact
	// relational tail while holding its writer transaction. R2 wins, so R1
	// cannot authorize the fork.
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	var tail sourceBoundary
	if err := conn.QueryRowContext(ctx, `SELECT id,source_prefix_digest,source_end_offset
		FROM worker_report
		WHERE attempt_id=?
		ORDER BY source_end_offset DESC,id
		LIMIT 1`, attempt.ID).Scan(&tail.id, &tail.digest, &tail.offset); err != nil {
		t.Fatal(err)
	}
	if tail == checkpoint {
		t.Fatal("restored R1 checkpoint unexpectedly equals canonical relational tail")
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		t.Fatal(err)
	}
	var rowsAfterRelock int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM worker_report WHERE attempt_id=?`, attempt.ID).
		Scan(&rowsAfterRelock); err != nil {
		t.Fatal(err)
	}
	if rowsAfterRelock != 2 {
		t.Fatalf("WorkerReport rows after relational-tail refusal = %d, want 2", rowsAfterRelock)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
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
