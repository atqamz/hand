package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
)

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
