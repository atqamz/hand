package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
)

const canonicalV19TaskArchiveDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestCanonicalV19TaskArchivePersistsOneImmutableFactAcrossRestart(t *testing.T) {
	home := canonicalV19TaskArchiveTerminalFixture(t)
	db, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	canonicalV19InsertTaskArchive(t, db, "task-1")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var actorKind, actorRef, archivedAt, reason, evidenceDigest string
	if err := db.QueryRow(`SELECT actor_kind,actor_ref,archived_at,reason,evidence_digest
		FROM task_archive WHERE task_id='task-1'`).Scan(
		&actorKind, &actorRef, &archivedAt, &reason, &evidenceDigest,
	); err != nil {
		t.Fatal(err)
	}
	if actorKind != "operator" || actorRef != "operator-1" || archivedAt != "2026-09-15T01:02:03Z" ||
		reason != "completed and reconciled" || evidenceDigest != canonicalV19TaskArchiveDigest {
		t.Fatalf("Task archive fact = %q/%q/%q/%q/%q", actorKind, actorRef, archivedAt, reason, evidenceDigest)
	}

	for label, statement := range map[string]string{
		"update": `UPDATE task_archive SET reason='changed' WHERE task_id='task-1'`,
		"delete": `DELETE FROM task_archive WHERE task_id='task-1'`,
		"duplicate": `INSERT INTO task_archive(task_id,actor_kind,actor_ref,archived_at,reason,evidence_digest)
			VALUES('task-1','supervisor','supervisor-2','2026-09-15T01:02:04Z','conflict','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb')`,
	} {
		t.Run(label, func(t *testing.T) {
			if _, err := db.Exec(statement); err == nil {
				t.Fatalf("%s archive fact succeeded", label)
			}
		})
	}
}

func TestCanonicalV19TaskArchiveRequiresTerminalTaskAndLineage(t *testing.T) {
	t.Run("active Task", func(t *testing.T) {
		fixture := canonicalV19PlanWriterFixture(t, "")
		db := canonicalV19TaskArchiveOpen(t, fixture.Home)
		defer func() { _ = db.Close() }()
		canonicalV19ExpectTaskArchiveRejected(t, db, "task-1")
	})

	t.Run("active Plan", func(t *testing.T) {
		fixture := canonicalV19AttemptWriterFixture(t)
		db := canonicalV19TaskArchiveOpen(t, fixture.Home)
		defer func() { _ = db.Close() }()
		if _, err := db.Exec(`UPDATE task SET lifecycle='abandoned',terminal_at='2026-09-15T01:00:00Z' WHERE id='task-1'`); err != nil {
			t.Fatal(err)
		}
		canonicalV19ExpectTaskArchiveRejected(t, db, "task-1")
	})

	t.Run("active Attempt", func(t *testing.T) {
		fixture := canonicalV19AttemptWriterFixture(t)
		if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptWriterInput("attempt-1", "plan-root")); err != nil {
			t.Fatal(err)
		}
		db := canonicalV19TaskArchiveOpen(t, fixture.Home)
		defer func() { _ = db.Close() }()
		if _, err := db.Exec(`UPDATE plan SET lifecycle='abandoned',terminal_at='2026-09-15T01:00:00Z' WHERE id='plan-root';
			UPDATE task SET lifecycle='abandoned',terminal_at='2026-09-15T01:00:01Z' WHERE id='task-1'`); err != nil {
			t.Fatal(err)
		}
		canonicalV19ExpectTaskArchiveRejected(t, db, "task-1")
	})
}

func TestCanonicalV19TaskArchiveRejectsUnresolvedTaskObligations(t *testing.T) {
	tests := map[string]func(*testing.T, *sql.DB){
		"external operation": func(t *testing.T, db *sql.DB) {
			_, err := db.Exec(`INSERT INTO external_operation(
				id,kind,adapter_ref,operation_key,request_digest,project_id,task_id,
				primary_scope_kind,primary_scope_key,created_at,state_changed_at
			) VALUES('operation-1','publication','adapter','key','digest','project-1','task-1',
				'publication','artifact-1','2026-09-15T01:01:00Z','2026-09-15T01:01:00Z')`)
			if err != nil {
				t.Fatal(err)
			}
		},
		"TaskHold": func(t *testing.T, db *sql.DB) {
			_, err := db.Exec(`INSERT INTO task_hold(id,task_id,ordinal,kind,reason,evidence_digest,created_at)
				VALUES('hold-1','task-1',1,'operator','wait','digest','2026-09-15T01:01:00Z')`)
			if err != nil {
				t.Fatal(err)
			}
		},
		"Decision": func(t *testing.T, db *sql.DB) {
			_, err := db.Exec(`INSERT INTO decision(id,task_id,scope_kind,question,created_at)
				VALUES('decision-1','task-1','task','choose','2026-09-15T01:01:00Z')`)
			if err != nil {
				t.Fatal(err)
			}
		},
		"Repair": func(t *testing.T, db *sql.DB) {
			_, err := db.Exec(`BEGIN IMMEDIATE;
				INSERT INTO repair_target(repair_id,task_id) VALUES('repair-1','task-1');
				INSERT INTO repair(id,repair_code,reason,evidence_digest,created_at)
				VALUES('repair-1','task-check','inspect','digest','2026-09-15T01:01:00Z');
				COMMIT`)
			if err != nil {
				t.Fatal(err)
			}
		},
	}

	for name, addObligation := range tests {
		t.Run(name, func(t *testing.T) {
			home := canonicalV19TaskArchiveTerminalFixture(t)
			db := canonicalV19TaskArchiveOpen(t, home)
			defer func() { _ = db.Close() }()
			addObligation(t, db)
			canonicalV19ExpectTaskArchiveRejected(t, db, "task-1")
		})
	}
}

func TestCanonicalV19TaskArchiveAllowsHistoricalUnacknowledgedWorkingReport(t *testing.T) {
	home := canonicalV19TaskArchiveTerminalFixture(t)
	db := canonicalV19TaskArchiveOpen(t, home)
	defer func() { _ = db.Close() }()
	canonicalV19InsertUnacknowledgedWorkerReport(t, db, "working")

	canonicalV19InsertTaskArchive(t, db, "task-1")

	var archiveCount, reportCount int
	if err := db.QueryRow(`SELECT
		(SELECT count(*) FROM task_archive WHERE task_id='task-1'),
		(SELECT count(*) FROM worker_report WHERE id='report-1')`).Scan(
		&archiveCount, &reportCount,
	); err != nil {
		t.Fatal(err)
	}
	if archiveCount != 1 || reportCount != 1 {
		t.Fatalf("archive/report rows = %d/%d, want 1/1", archiveCount, reportCount)
	}
}

func TestCanonicalV19TaskArchiveRejectsHandlingWorthyUnacknowledgedReportStates(t *testing.T) {
	for _, reportState := range []string{"paused", "blocked", "needs-decision", "done", "failed"} {
		t.Run(reportState, func(t *testing.T) {
			home := canonicalV19TaskArchiveTerminalFixture(t)
			db := canonicalV19TaskArchiveOpen(t, home)
			defer func() { _ = db.Close() }()
			canonicalV19InsertUnacknowledgedWorkerReport(t, db, reportState)

			canonicalV19ExpectTaskArchiveRejected(t, db, "task-1")
		})
	}
}

func TestCanonicalV19TaskArchiveRejectsOpenExecutionResources(t *testing.T) {
	fixture, _, _ := canonicalV19ExecutorBindingFixture(t)
	db := canonicalV19TaskArchiveOpen(t, fixture.Home)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`UPDATE attempt SET lifecycle='failed',terminal_at='2026-09-15T01:00:00Z' WHERE id='attempt-1';
		UPDATE plan SET lifecycle='abandoned',terminal_at='2026-09-15T01:00:01Z' WHERE id='plan-root';
		UPDATE task SET lifecycle='abandoned',terminal_at='2026-09-15T01:00:02Z' WHERE id='task-1'`); err != nil {
		t.Fatal(err)
	}
	canonicalV19ExpectTaskArchiveRejected(t, db, "task-1")
}

func TestCanonicalV19TaskArchiveDoesNotHideLaterEvidence(t *testing.T) {
	home := canonicalV19TaskArchiveTerminalFixture(t)
	db := canonicalV19TaskArchiveOpen(t, home)
	defer func() { _ = db.Close() }()
	canonicalV19InsertTaskArchive(t, db, "task-1")
	if _, err := db.Exec(`INSERT INTO worker_report(
		id,attempt_id,source_prefix_digest,source_end_offset,report_state,note,created_at
	) VALUES('report-later','attempt-1','prefix-later',2,'failed','late evidence','2026-09-15T01:03:00Z')`); err != nil {
		t.Fatal(err)
	}
	var lifecycle string
	var archiveCount, reportCount int
	if err := db.QueryRow(`SELECT lifecycle,
		(SELECT count(*) FROM task_archive WHERE task_id=task.id),
		(SELECT count(*) FROM worker_report wr JOIN attempt a ON a.id=wr.attempt_id
		 JOIN plan p ON p.id=a.plan_id WHERE p.task_id=task.id)
		FROM task WHERE id='task-1'`).Scan(&lifecycle, &archiveCount, &reportCount); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "satisfied" || archiveCount != 1 || reportCount != 1 {
		t.Fatalf("late evidence projection = lifecycle %q archive %d reports %d", lifecycle, archiveCount, reportCount)
	}
}

func TestCanonicalV19TaskArchiveConcurrentInsertKeepsOneFact(t *testing.T) {
	home := canonicalV19TaskArchiveTerminalFixture(t)
	db1 := canonicalV19TaskArchiveOpen(t, home)
	defer func() { _ = db1.Close() }()
	db2 := canonicalV19TaskArchiveOpen(t, home)
	defer func() { _ = db2.Close() }()

	start := make(chan struct{})
	errors := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	insert := func(db *sql.DB, actorRef, digest string) {
		ready.Done()
		<-start
		conn, err := db.Conn(context.Background())
		if err != nil {
			errors <- err
			return
		}
		defer func() { _ = conn.Close() }()
		if _, err = conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err == nil {
			_, err = conn.ExecContext(context.Background(), `INSERT INTO task_archive(
				task_id,actor_kind,actor_ref,archived_at,reason,evidence_digest
			) VALUES('task-1','supervisor',?,'2026-09-15T01:02:03Z','concurrent archive',?)`, actorRef, digest)
			if err == nil {
				_, err = conn.ExecContext(context.Background(), `COMMIT`)
			} else {
				_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
			}
		}
		errors <- err
	}
	go insert(db1, "supervisor-1", strings.Repeat("b", 64))
	go insert(db2, "supervisor-2", strings.Repeat("c", 64))
	ready.Wait()
	close(start)

	succeeded := 0
	for range 2 {
		if err := <-errors; err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent archive successes = %d, want 1", succeeded)
	}
	var count int
	if err := db1.QueryRow(`SELECT count(*) FROM task_archive WHERE task_id='task-1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("concurrent archive rows = %d, want 1", count)
	}
}

func canonicalV19TaskArchiveTerminalFixture(t *testing.T) string {
	t.Helper()
	fixture := canonicalV19AttemptWriterFixture(t)
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, canonicalV19AttemptWriterInput("attempt-1", "plan-root")); err != nil {
		t.Fatal(err)
	}
	db := canonicalV19TaskArchiveOpen(t, fixture.Home)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`UPDATE attempt SET lifecycle='completed',terminal_at='2026-09-15T01:00:00Z' WHERE id='attempt-1';
		UPDATE plan SET lifecycle='satisfied',terminal_at='2026-09-15T01:00:01Z' WHERE id='plan-root';
		UPDATE task SET lifecycle='satisfied',terminal_at='2026-09-15T01:00:02Z' WHERE id='task-1'`); err != nil {
		t.Fatal(err)
	}
	return fixture.Home
}

func canonicalV19TaskArchiveOpen(t *testing.T, home string) *sql.DB {
	t.Helper()
	db, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func canonicalV19InsertTaskArchive(t *testing.T, db *sql.DB, taskID string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO task_archive(task_id,actor_kind,actor_ref,archived_at,reason,evidence_digest)
		VALUES(?,'operator','operator-1','2026-09-15T01:02:03Z','completed and reconciled',?)`,
		taskID, canonicalV19TaskArchiveDigest); err != nil {
		t.Fatal(err)
	}
}

func canonicalV19InsertUnacknowledgedWorkerReport(t *testing.T, db *sql.DB, reportState string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO worker_report(
		id,attempt_id,source_prefix_digest,source_end_offset,report_state,note,created_at
	) VALUES('report-1','attempt-1','prefix-1',1,?,'review me','2026-09-15T01:01:00Z')`,
		reportState); err != nil {
		t.Fatal(err)
	}
}

func canonicalV19ExpectTaskArchiveRejected(t *testing.T, db *sql.DB, taskID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO task_archive(task_id,actor_kind,actor_ref,archived_at,reason,evidence_digest)
		VALUES(?,'operator','operator-1','2026-09-15T01:02:03Z','completed and reconciled',?)`,
		taskID, canonicalV19TaskArchiveDigest)
	if err == nil {
		t.Fatal("Task archive unexpectedly succeeded")
	}
	var count int
	if queryErr := db.QueryRow(`SELECT count(*) FROM task_archive WHERE task_id=?`, taskID).Scan(&count); queryErr != nil {
		t.Fatal(queryErr)
	}
	if count != 0 {
		t.Fatalf("Task archive rows after refusal = %d, want 0", count)
	}
}

func TestCanonicalV19TaskArchiveBoundsEvidence(t *testing.T) {
	tests := map[string]struct {
		actorRef       string
		archivedAt     string
		reason         string
		evidenceDigest string
	}{
		"actor reference": {
			actorRef:       "x\x00" + strings.Repeat("x", 256),
			archivedAt:     "2026-09-15T01:02:03Z",
			reason:         "reason",
			evidenceDigest: canonicalV19TaskArchiveDigest,
		},
		"archive time": {
			actorRef:       "operator-1",
			archivedAt:     "x\x00" + strings.Repeat("x", 64),
			reason:         "reason",
			evidenceDigest: canonicalV19TaskArchiveDigest,
		},
		"reason": {
			actorRef:       "operator-1",
			archivedAt:     "2026-09-15T01:02:03Z",
			reason:         "x\x00" + strings.Repeat("x", 1024),
			evidenceDigest: canonicalV19TaskArchiveDigest,
		},
		"oversized evidence digest": {
			actorRef:       "operator-1",
			archivedAt:     "2026-09-15T01:02:03Z",
			reason:         "reason",
			evidenceDigest: canonicalV19TaskArchiveDigest + "\x00junk",
		},
		"NUL-terminated evidence digest": {
			actorRef:       "operator-1",
			archivedAt:     "2026-09-15T01:02:03Z",
			reason:         "reason",
			evidenceDigest: strings.Repeat("a", 63) + "\x00",
		},
		"NUL-hidden evidence suffix": {
			actorRef:       "operator-1",
			archivedAt:     "2026-09-15T01:02:03Z",
			reason:         "reason",
			evidenceDigest: strings.Repeat("a", 62) + "\x00z",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			home := canonicalV19TaskArchiveTerminalFixture(t)
			db := canonicalV19TaskArchiveOpen(t, home)
			defer func() { _ = db.Close() }()
			_, err := db.Exec(`INSERT INTO task_archive(
				task_id,actor_kind,actor_ref,archived_at,reason,evidence_digest
			) VALUES('task-1','operator',?,?,?,?)`,
				test.actorRef, test.archivedAt, test.reason, test.evidenceDigest)
			if err == nil {
				t.Fatalf("oversized archive %s was accepted", name)
			}
		})
	}
}

func TestArchiveCanonicalV19PlanlessTerminalTaskReplaysExactFact(t *testing.T) {
	home := canonicalV19PlanlessTerminalTaskFixture(t)
	input := CanonicalV19TaskArchiveInput{
		TaskID: "task-1", ActorKind: "operator", ActorRef: "operator-1",
		ArchivedAt: "2026-09-15T01:02:03Z", Reason: "completed and reconciled",
		EvidenceDigest: canonicalV19TaskArchiveDigest,
	}
	for range 2 {
		if err := ArchiveCanonicalV19Task(context.Background(), home, input); err != nil {
			t.Fatal(err)
		}
	}
	db := canonicalV19TaskArchiveOpen(t, home)
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM task_archive WHERE task_id='task-1' AND actor_ref='operator-1' AND reason='completed and reconciled'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("archive facts = %d, want one exact fact", count)
	}
	input.Reason = "conflicting replay"
	if err := ArchiveCanonicalV19Task(context.Background(), home, input); err == nil {
		t.Fatal("conflicting archive replay succeeded")
	}
}

func TestArchiveCanonicalV19TaskAllowsTerminalLineageWithoutEffectsOrResources(t *testing.T) {
	home := canonicalV19TaskArchiveTerminalFixture(t)
	input := CanonicalV19TaskArchiveInput{
		TaskID: "task-1", ActorKind: "operator", ActorRef: "operator-1",
		ArchivedAt: "2026-09-15T01:02:03Z", Reason: "completed and reconciled",
		EvidenceDigest: canonicalV19TaskArchiveDigest,
	}
	if err := ArchiveCanonicalV19Task(context.Background(), home, input); err != nil {
		t.Fatal(err)
	}
	db := canonicalV19TaskArchiveOpen(t, home)
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM task_archive WHERE task_id='task-1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("resource-free terminal lineage archive facts = %d, want one", count)
	}
}

func TestArchiveCanonicalV19TaskRefusesOpenResourceLineageWithoutObservation(t *testing.T) {
	fixture, _, _ := canonicalV19ExecutorBindingFixture(t)
	db := canonicalV19TaskArchiveOpen(t, fixture.Home)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`UPDATE attempt SET lifecycle='failed',terminal_at='2026-09-15T01:00:00Z' WHERE id='attempt-1';
		UPDATE plan SET lifecycle='abandoned',terminal_at='2026-09-15T01:00:01Z' WHERE id='plan-root';
		UPDATE task SET lifecycle='abandoned',terminal_at='2026-09-15T01:00:02Z' WHERE id='task-1'`); err != nil {
		t.Fatal(err)
	}
	canonicalV19ExpectArchiveTaskNotEligible(t, fixture.Home)
}

func TestArchiveCanonicalV19TaskRefusesReleasedWorktreeLineageWithoutObservation(t *testing.T) {
	fixture, binding := canonicalV19WorktreeRemoveFixture(t)
	remove := canonicalV19WorktreeRemovePrepareInput(binding, "operation-remove-1")
	if _, err := PrepareCanonicalV19WorktreeRemove(context.Background(), fixture.Home, remove); err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19WorktreeRemove(context.Background(), fixture.Home, CanonicalV19WorktreeRemovedEvidence{
		OperationID: remove.OperationID, RemovedAt: "2026-09-05T15:04:00Z", EvidenceDigest: "remove-positive-absence-1",
	}); err != nil {
		t.Fatal(err)
	}
	db := canonicalV19TaskArchiveOpen(t, fixture.Home)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`UPDATE attempt SET lifecycle='completed',terminal_at='2026-09-15T01:00:00Z' WHERE id='attempt-1';
		UPDATE plan SET lifecycle='satisfied',terminal_at='2026-09-15T01:00:01Z' WHERE id='plan-root';
		UPDATE task SET lifecycle='satisfied',terminal_at='2026-09-15T01:00:02Z' WHERE id='task-1'`); err != nil {
		t.Fatal(err)
	}
	canonicalV19ExpectArchiveTaskNotEligible(t, fixture.Home)
}

func canonicalV19ExpectArchiveTaskNotEligible(t *testing.T, home string) {
	t.Helper()
	err := ArchiveCanonicalV19Task(context.Background(), home, CanonicalV19TaskArchiveInput{
		TaskID: "task-1", ActorKind: "operator", ActorRef: "operator-1",
		ArchivedAt: "2026-09-15T01:02:03Z", Reason: "completed and reconciled",
		EvidenceDigest: canonicalV19TaskArchiveDigest,
	})
	if !errors.Is(err, ErrCanonicalV19TaskArchiveNotEligible) {
		t.Fatalf("archive error = %v, want %v", err, ErrCanonicalV19TaskArchiveNotEligible)
	}
	db := canonicalV19TaskArchiveOpen(t, home)
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM task_archive`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("archive facts after refusal = %d, want zero", count)
	}
}

func TestArchiveCanonicalV19TaskConcurrentExactReplayConverges(t *testing.T) {
	home := canonicalV19PlanlessTerminalTaskFixture(t)
	input := CanonicalV19TaskArchiveInput{
		TaskID: "task-1", ActorKind: "operator", ActorRef: "operator-1",
		ArchivedAt: "2026-09-15T01:02:03Z", Reason: "completed and reconciled",
		EvidenceDigest: canonicalV19TaskArchiveDigest,
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			results <- ArchiveCanonicalV19Task(context.Background(), home, input)
		}()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	db := canonicalV19TaskArchiveOpen(t, home)
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM task_archive WHERE task_id='task-1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("concurrent archive facts = %d, want one", count)
	}
}

func canonicalV19PlanlessTerminalTaskFixture(t *testing.T) string {
	t.Helper()
	home := canonicalV19TaskWriterFixture(t, false)
	if _, err := CreateCanonicalV19Task(context.Background(), home, CanonicalV19TaskCreateInput{
		ID: "task-1", ProjectID: "project-1", Goal: "finish", GoalDigest: "goal-digest", CreatedAt: "2026-09-15T01:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	db := canonicalV19TaskArchiveOpen(t, home)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`UPDATE task SET lifecycle='abandoned',terminal_at='2026-09-15T01:01:00Z' WHERE id='task-1'`); err != nil {
		t.Fatal(err)
	}
	return home
}
