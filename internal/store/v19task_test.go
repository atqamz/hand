package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestSupersedeCanonicalV19TaskRecordsExactSuccessor(t *testing.T) {
	home := canonicalV19TaskWriterFixture(t, false)
	if _, err := CreateCanonicalV19Task(context.Background(), home, CanonicalV19TaskCreateInput{
		ID: "task-1", ProjectID: "project-1", Goal: "old goal", GoalDigest: "old-digest", CreatedAt: "2026-09-24T01:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	input := CanonicalV19TaskSupersedeInput{
		PredecessorTaskID: "task-1", SuccessorTaskID: "task-2", Goal: "revised goal", GoalDigest: "new-digest", At: "2026-09-24T01:01:00Z",
	}
	ordinal, err := SupersedeCanonicalV19Task(context.Background(), home, input)
	if err != nil || ordinal != 2 {
		t.Fatalf("supersede Task = %d, %v, want ordinal 2", ordinal, err)
	}
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var oldLifecycle, oldTerminal, newProject, newGoal, newDigest, newPredecessor, newLifecycle, newCreated string
	if err := db.sql.QueryRow(`SELECT lifecycle,terminal_at FROM task WHERE id='task-1'`).Scan(&oldLifecycle, &oldTerminal); err != nil {
		t.Fatal(err)
	}
	if err := db.sql.QueryRow(`SELECT project_id,goal,goal_digest,supersedes_task_id,lifecycle,created_at
		FROM task WHERE id='task-2'`).Scan(&newProject, &newGoal, &newDigest, &newPredecessor, &newLifecycle, &newCreated); err != nil {
		t.Fatal(err)
	}
	if oldLifecycle != "superseded" || oldTerminal != input.At || newProject != "project-1" ||
		newGoal != input.Goal || newDigest != input.GoalDigest || newPredecessor != input.PredecessorTaskID ||
		newLifecycle != "active" || newCreated != input.At {
		t.Fatalf("old/new Task = %q/%q, %q/%q/%q/%q/%q/%q", oldLifecycle, oldTerminal,
			newProject, newGoal, newDigest, newPredecessor, newLifecycle, newCreated)
	}
	if _, err := SupersedeCanonicalV19Task(context.Background(), home, input); !errors.Is(err, ErrCanonicalV19TaskNotCurrent) {
		t.Fatalf("replay = %v, want stale predecessor", err)
	}
	if got := canonicalV19TaskWriterCount(t, home); got != 2 {
		t.Fatalf("Task count after replay = %d, want 2", got)
	}
}

func TestSupersedeCanonicalV19TaskRejectsMalformedTimestamp(t *testing.T) {
	home := canonicalV19TaskWriterFixture(t, false)
	if _, err := CreateCanonicalV19Task(context.Background(), home, CanonicalV19TaskCreateInput{
		ID: "task-1", ProjectID: "project-1", Goal: "old goal", GoalDigest: "old-digest", CreatedAt: "2026-09-24T01:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	_, err := SupersedeCanonicalV19Task(context.Background(), home, CanonicalV19TaskSupersedeInput{
		PredecessorTaskID: "task-1", SuccessorTaskID: "task-2", Goal: "new goal", GoalDigest: "new-digest", At: "not-a-timestamp",
	})
	if err == nil {
		t.Fatal("malformed terminal timestamp succeeded")
	}
	if got := canonicalV19TaskWriterCount(t, home); got != 1 {
		t.Fatalf("Task count after malformed timestamp = %d, want 1", got)
	}
}

func TestSupersedeCanonicalV19TaskRollsBackOnSuccessorIdentityConflict(t *testing.T) {
	home := canonicalV19TaskWriterFixture(t, false)
	for _, id := range []string{"task-1", "task-2"} {
		if _, err := CreateCanonicalV19Task(context.Background(), home, CanonicalV19TaskCreateInput{
			ID: id, ProjectID: "project-1", Goal: id, GoalDigest: "digest", CreatedAt: "2026-09-24T01:00:00Z",
		}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := SupersedeCanonicalV19Task(context.Background(), home, CanonicalV19TaskSupersedeInput{
		PredecessorTaskID: "task-1", SuccessorTaskID: "task-2", Goal: "replacement", GoalDigest: "digest", At: "2026-09-24T01:01:00Z",
	})
	if !errors.Is(err, ErrCanonicalV19TaskConflict) {
		t.Fatalf("duplicate successor = %v, want conflict", err)
	}
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var lifecycle, terminalAt string
	if err := db.sql.QueryRow(`SELECT lifecycle,terminal_at FROM task WHERE id='task-1'`).Scan(&lifecycle, &terminalAt); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "active" || terminalAt != "" || canonicalV19TaskWriterCount(t, home) != 2 {
		t.Fatalf("predecessor after duplicate successor = %q/%q", lifecycle, terminalAt)
	}
}

func TestSupersedeCanonicalV19TaskRefusesRetiredProject(t *testing.T) {
	home := canonicalV19TaskWriterFixture(t, false)
	if _, err := CreateCanonicalV19Task(context.Background(), home, CanonicalV19TaskCreateInput{
		ID: "task-1", ProjectID: "project-1", Goal: "old goal", GoalDigest: "digest", CreatedAt: "2026-09-24T01:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	db, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE project SET retired_at='2026-09-24T01:00:30Z' WHERE id='project-1'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = SupersedeCanonicalV19Task(context.Background(), home, CanonicalV19TaskSupersedeInput{
		PredecessorTaskID: "task-1", SuccessorTaskID: "task-2", Goal: "new goal", GoalDigest: "digest", At: "2026-09-24T01:01:00Z",
	})
	if !errors.Is(err, ErrCanonicalV19ProjectNotCurrent) {
		t.Fatalf("retired Project supersession = %v, want stale Project", err)
	}
	if got := canonicalV19TaskWriterCount(t, home); got != 1 {
		t.Fatalf("Task count after retired Project = %d, want 1", got)
	}
}

func TestSupersedeCanonicalV19TaskRefusesExistingLineage(t *testing.T) {
	for _, tc := range []struct {
		name string
		seed func(*testing.T, string)
	}{
		{"TaskHold", func(t *testing.T, home string) {
			db, err := open(Path(home))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			if _, err := db.Exec(`INSERT INTO task_hold(id,task_id,ordinal,kind,reason,evidence_digest,created_at)
				VALUES('hold-1','task-1',1,'operator','wait','digest','2026-09-24T01:00:30Z')`); err != nil {
				t.Fatal(err)
			}
		}},
		{"Decision", func(t *testing.T, home string) {
			db, err := open(Path(home))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			if _, err := db.Exec(`INSERT INTO decision(id,task_id,scope_kind,question,created_at)
				VALUES('decision-1','task-1','task','choose','2026-09-24T01:00:30Z')`); err != nil {
				t.Fatal(err)
			}
		}},
		{"Repair", func(t *testing.T, home string) {
			db, err := open(Path(home))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			if _, err := db.Exec(`BEGIN IMMEDIATE;
				INSERT INTO repair_target(repair_id,task_id) VALUES('repair-1','task-1');
				INSERT INTO repair(id,repair_code,reason,evidence_digest,created_at)
				VALUES('repair-1','task-check','inspect','digest','2026-09-24T01:00:30Z');
				COMMIT`); err != nil {
				t.Fatal(err)
			}
		}},
		{"ExternalOperation", func(t *testing.T, home string) {
			db, err := open(Path(home))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			if _, err := db.Exec(`INSERT INTO external_operation(
				id,kind,adapter_ref,operation_key,request_digest,project_id,task_id,
				primary_scope_kind,primary_scope_key,created_at,state_changed_at
			) VALUES('operation-1','publication','adapter','key','digest','project-1','task-1',
				'publication','artifact-1','2026-09-24T01:00:30Z','2026-09-24T01:00:30Z')`); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := canonicalV19TaskWriterFixture(t, false)
			if _, err := CreateCanonicalV19Task(context.Background(), home, CanonicalV19TaskCreateInput{
				ID: "task-1", ProjectID: "project-1", Goal: "old goal", GoalDigest: "old-digest", CreatedAt: "2026-09-24T01:00:00Z",
			}); err != nil {
				t.Fatal(err)
			}
			tc.seed(t, home)
			_, err := SupersedeCanonicalV19Task(context.Background(), home, CanonicalV19TaskSupersedeInput{
				PredecessorTaskID: "task-1", SuccessorTaskID: "task-2", Goal: "new goal", GoalDigest: "new-digest", At: "2026-09-24T01:01:00Z",
			})
			if !errors.Is(err, ErrCanonicalV19TaskNotCurrent) {
				t.Fatalf("supersede with %s = %v, want conflict", tc.name, err)
			}
			if got := canonicalV19TaskWriterCount(t, home); got != 1 {
				t.Fatalf("Task count = %d, want 1", got)
			}
		})
	}
}

func TestSupersedeCanonicalV19TaskRefusesPlanHistory(t *testing.T) {
	home := canonicalV19TaskWriterFixture(t, false)
	if _, err := CreateCanonicalV19Task(context.Background(), home, CanonicalV19TaskCreateInput{
		ID: "task-1", ProjectID: "project-1", Goal: "old goal", GoalDigest: "old-digest", CreatedAt: "2026-09-24T01:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	db, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO workspace_binding(
		id,project_id,ordinal,repository_locator,repository_identity_digest,common_git_dir,physical_identity_digest,revision,established_at
	) VALUES('workspace-1','project-1',1,'repo','repo-digest','gitdir','physical-digest','revision','2026-09-24T01:00:00Z');
		INSERT INTO policy_revision(id,project_id,ordinal,policy_digest,created_at)
		VALUES('policy-1','project-1',1,'policy-digest','2026-09-24T01:00:00Z');
		INSERT INTO plan(id,task_id,ordinal,lineage_kind,intent,judgment,basis,brief,brief_digest,workspace_binding_id,policy_revision_id,created_at)
		VALUES('plan-1','task-1',1,'root','explore','bounded','basis','brief','brief-digest','workspace-1','policy-1','2026-09-24T01:00:30Z')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = SupersedeCanonicalV19Task(context.Background(), home, CanonicalV19TaskSupersedeInput{
		PredecessorTaskID: "task-1", SuccessorTaskID: "task-2", Goal: "new goal", GoalDigest: "new-digest", At: "2026-09-24T01:01:00Z",
	})
	if !errors.Is(err, ErrCanonicalV19TaskNotCurrent) {
		t.Fatalf("supersede with Plan history = %v, want conflict", err)
	}
	if got := canonicalV19TaskWriterCount(t, home); got != 1 {
		t.Fatalf("Task count = %d, want 1", got)
	}
}

func TestSupersedeCanonicalV19TaskRefusesInboundOpenHold(t *testing.T) {
	home := canonicalV19TaskWriterFixture(t, false)
	for _, id := range []string{"task-1", "task-blocked"} {
		if _, err := CreateCanonicalV19Task(context.Background(), home, CanonicalV19TaskCreateInput{
			ID: id, ProjectID: "project-1", Goal: id, GoalDigest: "digest", CreatedAt: "2026-09-24T01:00:00Z",
		}); err != nil {
			t.Fatal(err)
		}
	}
	db, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO task_hold(id,task_id,ordinal,kind,reason,evidence_digest,created_at)
		VALUES('hold-1','task-blocked',1,'blocked','wait for task-1','digest','2026-09-24T01:00:30Z');
		INSERT INTO task_hold_blocked_on_task(hold_id,blocked_on_task_id) VALUES('hold-1','task-1')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = SupersedeCanonicalV19Task(context.Background(), home, CanonicalV19TaskSupersedeInput{
		PredecessorTaskID: "task-1", SuccessorTaskID: "task-3", Goal: "replacement", GoalDigest: "digest", At: "2026-09-24T01:01:00Z",
	})
	if !errors.Is(err, ErrCanonicalV19TaskNotCurrent) {
		t.Fatalf("supersede inbound blocked Task = %v, want conflict", err)
	}
	if got := canonicalV19TaskWriterCount(t, home); got != 2 {
		t.Fatalf("Task count = %d, want 2", got)
	}
	if err := ResolveCanonicalV19TaskHold(context.Background(), home, CanonicalV19TaskHoldResolveInput{
		HoldID: "hold-1", Resolution: "superseded", ResolvedAt: "2026-09-24T01:00:45Z", EvidenceDigest: "digest",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := SupersedeCanonicalV19Task(context.Background(), home, CanonicalV19TaskSupersedeInput{
		PredecessorTaskID: "task-1", SuccessorTaskID: "task-3", Goal: "replacement", GoalDigest: "digest", At: "2026-09-24T01:01:00Z",
	}); err != nil {
		t.Fatalf("resolved inbound Hold still blocked supersession: %v", err)
	}
}

func TestSupersedeCanonicalV19TaskConcurrentSuccessorsHaveOneWinner(t *testing.T) {
	home := canonicalV19TaskWriterFixture(t, false)
	if _, err := CreateCanonicalV19Task(context.Background(), home, CanonicalV19TaskCreateInput{
		ID: "task-1", ProjectID: "project-1", Goal: "old goal", GoalDigest: "old-digest", CreatedAt: "2026-09-24T01:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, id := range []string{"task-2", "task-3"} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := SupersedeCanonicalV19Task(context.Background(), home, CanonicalV19TaskSupersedeInput{
				PredecessorTaskID: "task-1", SuccessorTaskID: id, Goal: id, GoalDigest: "digest", At: "2026-09-24T01:01:00Z",
			})
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	successes, stale := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrCanonicalV19TaskNotCurrent):
			stale++
		default:
			t.Fatalf("concurrent supersede = %v", err)
		}
	}
	if successes != 1 || stale != 1 || canonicalV19TaskWriterCount(t, home) != 2 {
		t.Fatalf("concurrent winners/stale = %d/%d", successes, stale)
	}
}

func TestCreateCanonicalV19TaskAllocatesExactProjectOrdinal(t *testing.T) {
	home := canonicalV19TaskWriterFixture(t, false)
	ctx := context.Background()
	first := CanonicalV19TaskCreateInput{
		ID:         "task-1",
		ProjectID:  "project-1",
		Goal:       "ship the canonical writer",
		GoalDigest: "goal-digest-1",
		CreatedAt:  "2026-09-04T06:40:00Z",
	}
	ordinal, err := CreateCanonicalV19Task(ctx, home, first)
	if err != nil {
		t.Fatal(err)
	}
	if ordinal != 1 {
		t.Fatalf("first Task ordinal = %d, want 1", ordinal)
	}
	second := first
	second.ID = "task-2"
	second.Goal = "prove the next ordinal"
	second.GoalDigest = "goal-digest-2"
	ordinal, err = CreateCanonicalV19Task(ctx, home, second)
	if err != nil {
		t.Fatal(err)
	}
	if ordinal != 2 {
		t.Fatalf("second Task ordinal = %d, want 2", ordinal)
	}

	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var got CanonicalV19TaskCreateInput
	var gotOrdinal int64
	var supersedes sql.NullString
	var lifecycle, terminalAt string
	if err := db.sql.QueryRow(`SELECT id,project_id,ordinal,goal,goal_digest,supersedes_task_id,lifecycle,created_at,terminal_at
		FROM task WHERE id = ?`, first.ID).Scan(
		&got.ID, &got.ProjectID, &gotOrdinal, &got.Goal, &got.GoalDigest, &supersedes, &lifecycle, &got.CreatedAt, &terminalAt,
	); err != nil {
		t.Fatal(err)
	}
	if got != first || gotOrdinal != 1 || supersedes.Valid || lifecycle != "active" || terminalAt != "" {
		t.Fatalf("persisted Task = %#v ordinal=%d supersedes=%#v lifecycle=%q terminal_at=%q",
			got, gotOrdinal, supersedes, lifecycle, terminalAt)
	}
}

func TestCreateCanonicalV19TaskRefusesRetiredExactProject(t *testing.T) {
	home := canonicalV19TaskWriterFixture(t, true)
	_, err := CreateCanonicalV19Task(context.Background(), home, CanonicalV19TaskCreateInput{
		ID:         "task-retired",
		ProjectID:  "project-1",
		Goal:       "must not retarget",
		GoalDigest: "goal-digest",
		CreatedAt:  "2026-09-04T06:41:00Z",
	})
	if !errors.Is(err, ErrCanonicalV19ProjectNotCurrent) {
		t.Fatalf("retired Project error = %v, want %v", err, ErrCanonicalV19ProjectNotCurrent)
	}
	if got := canonicalV19TaskWriterCount(t, home); got != 0 {
		t.Fatalf("Task rows after retired Project refusal = %d, want 0", got)
	}
}

func TestCreateCanonicalV19TaskDuplicateIdentityDoesNotRetarget(t *testing.T) {
	home := canonicalV19TaskWriterFixture(t, false)
	input := CanonicalV19TaskCreateInput{
		ID:         "task-same",
		ProjectID:  "project-1",
		Goal:       "first immutable goal",
		GoalDigest: "goal-digest-first",
		CreatedAt:  "2026-09-04T06:42:00Z",
	}
	if _, err := CreateCanonicalV19Task(context.Background(), home, input); err != nil {
		t.Fatal(err)
	}
	input.Goal = "replacement goal must lose"
	input.GoalDigest = "goal-digest-replacement"
	if _, err := CreateCanonicalV19Task(context.Background(), home, input); !errors.Is(err, ErrCanonicalV19TaskConflict) {
		t.Fatalf("duplicate Task error = %v, want %v", err, ErrCanonicalV19TaskConflict)
	}
	if got := canonicalV19TaskWriterCount(t, home); got != 1 {
		t.Fatalf("Task rows after duplicate = %d, want 1", got)
	}

	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var goal, digest string
	var ordinal int64
	if err := db.sql.QueryRow(`SELECT ordinal,goal,goal_digest FROM task WHERE id='task-same'`).Scan(&ordinal, &goal, &digest); err != nil {
		t.Fatal(err)
	}
	if ordinal != 1 || goal != "first immutable goal" || digest != "goal-digest-first" {
		t.Fatalf("duplicate retargeted Task: ordinal=%d goal=%q digest=%q", ordinal, goal, digest)
	}
}

func TestCreateCanonicalV19TaskRefusesLegacyWithoutMutation(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	before, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	_, err = CreateCanonicalV19Task(context.Background(), home, CanonicalV19TaskCreateInput{
		ID:         "task-canonical",
		ProjectID:  "project-1",
		Goal:       "must not run the legacy migration ladder",
		GoalDigest: "goal-digest",
		CreatedAt:  "2026-09-04T06:43:00Z",
	})
	if !errors.Is(err, ErrCanonicalV19SchemaMismatch) {
		t.Fatalf("legacy-family writer error = %v, want %v", err, ErrCanonicalV19SchemaMismatch)
	}
	after, digestErr := legacyV18CutoverFileSHA256(Path(home))
	if digestErr != nil {
		t.Fatal(digestErr)
	}
	if after != before {
		t.Fatalf("canonical writer mutated legacy bytes: before=%s after=%s", before, after)
	}
}

func TestCreateCanonicalV19TaskDoesNotCreateOrRepairDatabase(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		home := t.TempDir()
		_, err := CreateCanonicalV19Task(context.Background(), home, CanonicalV19TaskCreateInput{
			ID: "task-1", ProjectID: "project-1", Goal: "goal", GoalDigest: "digest", CreatedAt: "now",
		})
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing database error = %v, want os.ErrNotExist", err)
		}
		if _, statErr := os.Lstat(Path(home)); !os.IsNotExist(statErr) {
			t.Fatalf("missing database was created: stat error = %v", statErr)
		}
	})

	t.Run("schema drift", func(t *testing.T) {
		home := canonicalV19TaskWriterFixture(t, false)
		db, err := open(Path(home))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TABLE canonical_drift(id TEXT PRIMARY KEY) STRICT`); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		_, err = CreateCanonicalV19Task(context.Background(), home, CanonicalV19TaskCreateInput{
			ID: "task-1", ProjectID: "project-1", Goal: "goal", GoalDigest: "digest", CreatedAt: "now",
		})
		if !errors.Is(err, ErrCanonicalV19SchemaMismatch) {
			t.Fatalf("drifted schema error = %v, want %v", err, ErrCanonicalV19SchemaMismatch)
		}
		if got := canonicalV19TaskWriterCount(t, home); got != 0 {
			t.Fatalf("Task rows after schema-drift refusal = %d, want 0", got)
		}
	})
}

func canonicalV19TaskWriterFixture(t *testing.T, retired bool) string {
	t.Helper()
	home := t.TempDir()
	db, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := createCanonicalV19Schema(db); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO fleet(singleton,fleet_id,created_at) VALUES(1,'fleet-1','2026-09-04T06:39:00Z')`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	retiredAt := ""
	if retired {
		retiredAt = "2026-09-04T06:39:30Z"
	}
	if _, err := db.Exec(`INSERT INTO project(id,fleet_id,ordinal,display_name,created_at,retired_at)
		VALUES('project-1','fleet-1',1,'demo','2026-09-04T06:39:00Z',?)`, retiredAt); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return home
}

func canonicalV19TaskWriterCount(t *testing.T, home string) int {
	t.Helper()
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM task`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestAbandonCanonicalV19TaskCascadesNeverLaunchedAttemptAndPlanThenArchives(t *testing.T) {
	home := canonicalV19AttemptWriterFixture(t).Home
	ctx := context.Background()
	if _, err := CreateCanonicalV19Attempt(ctx, home, canonicalV19AttemptWriterInput("attempt-1", "plan-root")); err != nil {
		t.Fatal(err)
	}
	if err := AbandonCanonicalV19Task(ctx, home, "task-1", "2026-09-04T09:02:00Z"); err != nil {
		t.Fatal(err)
	}
	canonicalV19DecisionAssertCount(t, home, `SELECT (SELECT count(*) FROM attempt WHERE id='attempt-1' AND lifecycle='interrupted')+
		(SELECT count(*) FROM plan WHERE id='plan-root' AND lifecycle='abandoned')+
		(SELECT count(*) FROM task WHERE id='task-1' AND lifecycle='abandoned')+
		(SELECT count(*) FROM attempt WHERE terminal_at='2026-09-04T09:02:00Z')`, 4)
	if err := AbandonCanonicalV19Task(ctx, home, "task-1", "2026-09-04T09:03:00Z"); !errors.Is(err, ErrCanonicalV19TaskNotCurrent) {
		t.Fatalf("replayed abandon = %v, want %v", err, ErrCanonicalV19TaskNotCurrent)
	}
	if _, err := CreateCanonicalV19Attempt(ctx, home, canonicalV19AttemptWriterInput("attempt-2", "plan-root")); !errors.Is(err, ErrCanonicalV19AttemptNotCurrent) {
		t.Fatalf("retry under abandoned Task = %v, want %v", err, ErrCanonicalV19AttemptNotCurrent)
	}
	if err := ArchiveCanonicalV19Task(ctx, home, CanonicalV19TaskArchiveInput{
		TaskID: "task-1", ActorKind: "operator", ActorRef: "operator-1",
		ArchivedAt: "2026-09-04T09:04:00Z", Reason: "abandoned", EvidenceDigest: canonicalV19TaskArchiveDigest,
	}); err != nil {
		t.Fatalf("archive abandoned lineage: %v", err)
	}
}

func TestAbandonCanonicalV19TaskRefusesOpenObligationsWithoutMutation(t *testing.T) {
	planless := func(t *testing.T, query string, tasks ...string) string {
		home := canonicalV19TaskWriterFixture(t, false)
		for _, id := range append([]string{"task-1"}, tasks...) {
			if _, err := CreateCanonicalV19Task(context.Background(), home, CanonicalV19TaskCreateInput{
				ID: id, ProjectID: "project-1", Goal: id, GoalDigest: "digest", CreatedAt: "2026-09-04T07:59:00Z",
			}); err != nil {
				t.Fatal(err)
			}
		}
		canonicalV19RepairWriterExec(t, home, query)
		return home
	}
	withAttempt := func(t *testing.T, query string) string {
		home := canonicalV19AttemptWriterFixture(t).Home
		if _, err := CreateCanonicalV19Attempt(context.Background(), home, canonicalV19AttemptWriterInput("attempt-1", "plan-root")); err != nil {
			t.Fatal(err)
		}
		canonicalV19RepairWriterExec(t, home, query)
		return home
	}
	repair := func(column, target string) string {
		return `BEGIN IMMEDIATE;
			INSERT INTO repair_target(repair_id,` + column + `) VALUES('repair-1','` + target + `');
			INSERT INTO repair(id,repair_code,reason,evidence_digest,created_at)
			VALUES('repair-1','lineage-check','inspect','digest','2026-09-04T09:00:30Z');
			COMMIT`
	}
	for _, tc := range []struct {
		name, want string
		seed       func(*testing.T) string
	}{
		{"UnresolvedExternalOperation", "unresolved external operation", func(t *testing.T) string {
			return planless(t, `INSERT INTO external_operation(
				id,kind,adapter_ref,operation_key,request_digest,project_id,task_id,
				primary_scope_kind,primary_scope_key,created_at,state_changed_at
			) VALUES('operation-1','publication','adapter','key','digest','project-1','task-1',
				'publication','artifact-1','2026-09-04T08:00:00Z','2026-09-04T08:00:00Z')`)
		}},
		{"PreparedLaunchOnActiveAttempt", "unresolved external operation", func(t *testing.T) string {
			fixture, worktree, session := canonicalV19SessionBindingFixture(t)
			if _, err := PrepareCanonicalV19Launch(context.Background(), fixture.Home,
				canonicalV19LaunchPrepareInput(worktree, session, "operation-launch-1", "executor-binding-1")); err != nil {
				t.Fatal(err)
			}
			return fixture.Home
		}},
		{"OpenTaskHold", "open TaskHold", func(t *testing.T) string {
			return planless(t, `INSERT INTO task_hold(id,task_id,ordinal,kind,reason,evidence_digest,created_at)
				VALUES('hold-1','task-1',1,'operator','wait','digest','2026-09-04T08:00:00Z')`)
		}},
		{"InboundOpenTaskHold", "open inbound TaskHold", func(t *testing.T) string {
			return planless(t, `INSERT INTO task_hold(id,task_id,ordinal,kind,reason,evidence_digest,created_at)
				VALUES('hold-1','task-blocked',1,'blocked','wait for task-1','digest','2026-09-04T08:00:00Z');
				INSERT INTO task_hold_blocked_on_task(hold_id,blocked_on_task_id) VALUES('hold-1','task-1')`, "task-blocked")
		}},
		{"TaskRepair", "open Repair", func(t *testing.T) string {
			return planless(t, repair("task_id", "task-1"))
		}},
		{"PlanRepair", "open Repair", func(t *testing.T) string {
			home := canonicalV19AttemptWriterFixture(t).Home
			canonicalV19RepairWriterExec(t, home, repair("plan_id", "plan-root"))
			return home
		}},
		{"AttemptRepair", "open Repair", func(t *testing.T) string {
			return withAttempt(t, repair("attempt_id", "attempt-1"))
		}},
		{"OpenAttemptBackoff", "open AttemptBackoff", func(t *testing.T) string {
			return withAttempt(t, `INSERT INTO attempt_backoff(id,attempt_id,ordinal,reason,not_before,evidence_digest,created_at)
				VALUES('backoff-1','attempt-1',1,'usage-limit','2026-09-04T10:00:00Z','digest','2026-09-04T09:00:30Z')`)
		}},
		{"OpenExecutorBinding", "open ExecutorBinding", func(t *testing.T) string {
			fixture, _, _ := canonicalV19ExecutorBindingFixture(t)
			return fixture.Home
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := tc.seed(t)
			err := AbandonCanonicalV19Task(context.Background(), home, "task-1", "2026-09-07T00:00:00Z")
			if !errors.Is(err, ErrCanonicalV19TaskNotCurrent) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("abandon with %s = %v, want %v naming %q", tc.name, err, ErrCanonicalV19TaskNotCurrent, tc.want)
			}
			canonicalV19DecisionAssertCount(t, home, `SELECT (SELECT count(*) FROM task WHERE lifecycle<>'active')+
				(SELECT count(*) FROM plan WHERE lifecycle<>'active')+(SELECT count(*) FROM attempt WHERE lifecycle<>'active')`, 0)
		})
	}
}

func TestAbandonCanonicalV19TaskRefusesRetiredProject(t *testing.T) {
	home := canonicalV19TaskWriterFixture(t, false)
	if _, err := CreateCanonicalV19Task(context.Background(), home, CanonicalV19TaskCreateInput{
		ID: "task-1", ProjectID: "project-1", Goal: "goal", GoalDigest: "digest", CreatedAt: "2026-09-04T07:59:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	canonicalV19RepairWriterExec(t, home, `UPDATE project SET retired_at='2026-09-04T08:00:00Z' WHERE id='project-1'`)
	if err := AbandonCanonicalV19Task(context.Background(), home, "task-1", "2026-09-04T08:01:00Z"); !errors.Is(err, ErrCanonicalV19ProjectNotCurrent) {
		t.Fatalf("abandon under retired Project = %v, want %v", err, ErrCanonicalV19ProjectNotCurrent)
	}
	canonicalV19DecisionAssertCount(t, home, `SELECT count(*) FROM task WHERE id='task-1' AND lifecycle='active'`, 1)
}

func TestAbandonCanonicalV19TaskRacesSupersedeWithOneWinner(t *testing.T) {
	home := canonicalV19TaskWriterFixture(t, false)
	if _, err := CreateCanonicalV19Task(context.Background(), home, CanonicalV19TaskCreateInput{
		ID: "task-1", ProjectID: "project-1", Goal: "old goal", GoalDigest: "old-digest", CreatedAt: "2026-09-24T01:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	errs := canonicalV19RaceWriters(
		func() error {
			return AbandonCanonicalV19Task(context.Background(), home, "task-1", "2026-09-24T01:01:00Z")
		},
		func() error {
			_, err := SupersedeCanonicalV19Task(context.Background(), home, CanonicalV19TaskSupersedeInput{
				PredecessorTaskID: "task-1", SuccessorTaskID: "task-2", Goal: "new goal", GoalDigest: "digest", At: "2026-09-24T01:01:00Z",
			})
			return err
		},
	)
	switch {
	case errs[0] == nil && errors.Is(errs[1], ErrCanonicalV19TaskNotCurrent):
		canonicalV19DecisionAssertCount(t, home, `SELECT count(*) FROM task WHERE id='task-1' AND lifecycle='abandoned'`, 1)
		canonicalV19DecisionAssertCount(t, home, `SELECT count(*) FROM task`, 1)
	case errs[1] == nil && errors.Is(errs[0], ErrCanonicalV19TaskNotCurrent):
		canonicalV19DecisionAssertCount(t, home, `SELECT count(*) FROM task WHERE id='task-1' AND lifecycle='superseded'`, 1)
		canonicalV19DecisionAssertCount(t, home, `SELECT count(*) FROM task`, 2)
	default:
		t.Fatalf("abandon/supersede = %v/%v, want one winner and one typed loser", errs[0], errs[1])
	}
}

func TestAbandonCanonicalV19TaskRacesTaskHoldInsertWithOneWinner(t *testing.T) {
	home := canonicalV19TaskWriterFixture(t, false)
	if _, err := CreateCanonicalV19Task(context.Background(), home, CanonicalV19TaskCreateInput{
		ID: "task-1", ProjectID: "project-1", Goal: "goal", GoalDigest: "digest", CreatedAt: "2026-09-05T02:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	errs := canonicalV19RaceWriters(
		func() error {
			return AbandonCanonicalV19Task(context.Background(), home, "task-1", "2026-09-05T03:00:00Z")
		},
		func() error {
			_, err := CreateCanonicalV19TaskHold(context.Background(), home, canonicalV19TaskHoldWriterInput("hold-1"))
			return err
		},
	)
	switch {
	case errs[0] == nil && errors.Is(errs[1], ErrCanonicalV19TaskHoldNotCurrent):
		canonicalV19DecisionAssertCount(t, home, `SELECT count(*) FROM task WHERE id='task-1' AND lifecycle='abandoned'`, 1)
		canonicalV19DecisionAssertCount(t, home, `SELECT count(*) FROM task_hold`, 0)
	case errs[1] == nil && errors.Is(errs[0], ErrCanonicalV19TaskNotCurrent):
		canonicalV19DecisionAssertCount(t, home, `SELECT count(*) FROM task WHERE id='task-1' AND lifecycle='active'`, 1)
		canonicalV19DecisionAssertCount(t, home, `SELECT count(*) FROM task_hold`, 1)
	default:
		t.Fatalf("abandon/hold = %v/%v, want one winner and one typed loser", errs[0], errs[1])
	}
}

func TestAbandonCanonicalV19TaskRacesRetryAndReplanWithoutRetarget(t *testing.T) {
	for _, tc := range []struct {
		name     string
		loserErr error
		writer   func(string) error
	}{
		{"Retry", ErrCanonicalV19AttemptNotCurrent, func(home string) error {
			_, err := CreateCanonicalV19Attempt(context.Background(), home, canonicalV19AttemptWriterInput("attempt-1", "plan-root"))
			return err
		}},
		{"Replan", ErrCanonicalV19PlanNotCurrent, func(home string) error {
			_, err := ReplanCanonicalV19Plan(context.Background(), home, CanonicalV19PlanReplanInput{
				PredecessorPlanID: "plan-root", Successor: canonicalV19PlanWriterInput("plan-replan"), SupersededAt: "2026-09-04T09:01:00Z",
			})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := canonicalV19AttemptWriterFixture(t).Home
			errs := canonicalV19RaceWriters(
				func() error {
					return AbandonCanonicalV19Task(context.Background(), home, "task-1", "2026-09-04T09:02:00Z")
				},
				func() error { return tc.writer(home) },
			)
			if errs[0] != nil || (errs[1] != nil && !errors.Is(errs[1], tc.loserErr)) {
				t.Fatalf("abandon/%s = %v/%v, want abandon to win and %s to commit first or lose typed", tc.name, errs[0], errs[1], tc.name)
			}
			canonicalV19DecisionAssertCount(t, home, `SELECT (SELECT count(*) FROM task WHERE lifecycle='active')+
				(SELECT count(*) FROM plan WHERE lifecycle='active')+(SELECT count(*) FROM attempt WHERE lifecycle='active')`, 0)
			canonicalV19DecisionAssertCount(t, home, `SELECT count(*) FROM plan WHERE lifecycle='abandoned'`, 1)
		})
	}
}

func TestAbandonCanonicalV19TaskRacesBlockedOnTaskHoldInsertWithOneWinner(t *testing.T) {
	home := canonicalV19TaskHoldWriterFixture(t)
	hold := canonicalV19TaskHoldWriterInput("hold-1")
	hold.Kind, hold.BlockedOnTaskID = "blocked", "task-2"
	errs := canonicalV19RaceWriters(
		func() error {
			_, err := CreateCanonicalV19TaskHold(context.Background(), home, hold)
			return err
		},
		func() error {
			return AbandonCanonicalV19Task(context.Background(), home, "task-2", "2026-09-05T03:00:01Z")
		},
	)
	holdErr, abandonErr := errs[0], errs[1]
	switch {
	case holdErr == nil && errors.Is(abandonErr, ErrCanonicalV19TaskNotCurrent):
		canonicalV19DecisionAssertCount(t, home, `SELECT count(*) FROM task WHERE id='task-2' AND lifecycle='active'`, 1)
		canonicalV19DecisionAssertCount(t, home, `SELECT count(*) FROM task_hold_blocked_on_task WHERE hold_id='hold-1' AND blocked_on_task_id='task-2'`, 1)
	case abandonErr == nil && errors.Is(holdErr, ErrCanonicalV19TaskHoldNotCurrent):
		canonicalV19DecisionAssertCount(t, home, `SELECT count(*) FROM task WHERE id='task-2' AND lifecycle='abandoned'`, 1)
		canonicalV19DecisionAssertCount(t, home, `SELECT count(*) FROM task_hold`, 0)
	default:
		t.Fatalf("blocked TaskHold/abandon race = %v / %v, want exactly one winner", holdErr, abandonErr)
	}
}
