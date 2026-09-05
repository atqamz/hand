package store

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestCreateCanonicalV19RepairPersistsSupportedTypedTargets(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home,
		canonicalV19AttemptWriterInput("attempt-1", "plan-root")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		target CanonicalV19RepairTarget
		column string
		id     string
	}{
		{name: "project", target: CanonicalV19RepairTarget{ProjectID: "project-1"}, column: "project_id", id: "project-1"},
		{name: "workspace", target: CanonicalV19RepairTarget{WorkspaceBindingID: "workspace-1"}, column: "workspace_binding_id", id: "workspace-1"},
		{name: "task", target: CanonicalV19RepairTarget{TaskID: "task-1"}, column: "task_id", id: "task-1"},
		{name: "plan", target: CanonicalV19RepairTarget{PlanID: "plan-root"}, column: "plan_id", id: "plan-root"},
		{name: "attempt", target: CanonicalV19RepairTarget{AttemptID: "attempt-1"}, column: "attempt_id", id: "attempt-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := canonicalV19RepairWriterInput("repair-"+tt.name, tt.target)
			if err := CreateCanonicalV19Repair(context.Background(), fixture.Home, input); err != nil {
				t.Fatal(err)
			}

			db, err := openReadOnly(fixture.Home)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			var code, reason, digest, createdAt, targetID string
			if err := db.sql.QueryRow(`SELECT repair_code,reason,evidence_digest,created_at FROM repair WHERE id=?`, input.ID).
				Scan(&code, &reason, &digest, &createdAt); err != nil {
				t.Fatal(err)
			}
			if code != input.RepairCode || reason != input.Reason || digest != input.EvidenceDigest || createdAt != input.CreatedAt {
				t.Fatalf("persisted Repair = code=%q reason=%q digest=%q created_at=%q", code, reason, digest, createdAt)
			}
			query := `SELECT ` + tt.column + ` FROM repair_target WHERE repair_id=?`
			if err := db.sql.QueryRow(query, input.ID).Scan(&targetID); err != nil {
				t.Fatal(err)
			}
			if targetID != tt.id {
				t.Fatalf("typed target = %q, want %q", targetID, tt.id)
			}
			var populated int
			if err := db.sql.QueryRow(`SELECT
				(project_id IS NOT NULL)+(workspace_binding_id IS NOT NULL)+(task_id IS NOT NULL)+
				(plan_id IS NOT NULL)+(attempt_id IS NOT NULL)+(external_operation_id IS NOT NULL)+
				(worktree_binding_id IS NOT NULL)+(session_binding_id IS NOT NULL)+(executor_binding_id IS NOT NULL)
				FROM repair_target WHERE repair_id=?`, input.ID).Scan(&populated); err != nil {
				t.Fatal(err)
			}
			if populated != 1 {
				t.Fatalf("populated Repair target columns = %d, want 1", populated)
			}
		})
	}
}

func TestCreateCanonicalV19RepairRequiresExactlyOneTypedTarget(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	for name, target := range map[string]CanonicalV19RepairTarget{
		"none": {},
		"two":  {ProjectID: "project-1", TaskID: "task-1"},
	} {
		t.Run(name, func(t *testing.T) {
			err := CreateCanonicalV19Repair(context.Background(), fixture.Home,
				canonicalV19RepairWriterInput("repair-"+name, target))
			if err == nil {
				t.Fatal("invalid target shape unexpectedly succeeded")
			}
			if got := canonicalV19RepairWriterCount(t, fixture.Home, "repair"); got != 0 {
				t.Fatalf("Repair rows after invalid target = %d, want 0", got)
			}
			if got := canonicalV19RepairWriterCount(t, fixture.Home, "repair_target"); got != 0 {
				t.Fatalf("Repair target rows after invalid target = %d, want 0", got)
			}
		})
	}
}

func TestCreateCanonicalV19RepairFutureTargetFamiliesFailClosed(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	targets := []CanonicalV19RepairTarget{
		{ExternalOperationID: "operation-1"},
		{WorktreeBindingID: "worktree-1"},
		{SessionBindingID: "session-1"},
		{ExecutorBindingID: "executor-1"},
	}
	for i, target := range targets {
		err := CreateCanonicalV19Repair(context.Background(), fixture.Home,
			canonicalV19RepairWriterInput("repair-future", target))
		if !errors.Is(err, ErrCanonicalV19RepairTargetUnsupported) {
			t.Fatalf("future target %d error = %v, want %v", i, err, ErrCanonicalV19RepairTargetUnsupported)
		}
	}
	if got := canonicalV19RepairWriterCount(t, fixture.Home, "repair"); got != 0 {
		t.Fatalf("Repair rows after unsupported targets = %d, want 0", got)
	}
}

func TestCreateCanonicalV19RepairRefusesStaleTypedTargets(t *testing.T) {
	t.Run("retired project", func(t *testing.T) {
		fixture := canonicalV19AttemptWriterFixture(t)
		canonicalV19RepairWriterExec(t, fixture.Home,
			`UPDATE project SET retired_at='2026-09-05T10:00:00Z' WHERE id='project-1'`)
		err := CreateCanonicalV19Repair(context.Background(), fixture.Home,
			canonicalV19RepairWriterInput("repair-project", CanonicalV19RepairTarget{ProjectID: "project-1"}))
		if !errors.Is(err, ErrCanonicalV19RepairNotCurrent) {
			t.Fatalf("retired Project error = %v, want %v", err, ErrCanonicalV19RepairNotCurrent)
		}
	})

	t.Run("superseded workspace", func(t *testing.T) {
		fixture := canonicalV19AttemptWriterFixture(t)
		canonicalV19RepairWriterExec(t, fixture.Home,
			`UPDATE workspace_binding SET superseded_at='2026-09-05T10:00:00Z' WHERE id='workspace-1'`)
		err := CreateCanonicalV19Repair(context.Background(), fixture.Home,
			canonicalV19RepairWriterInput("repair-workspace", CanonicalV19RepairTarget{WorkspaceBindingID: "workspace-1"}))
		if !errors.Is(err, ErrCanonicalV19RepairNotCurrent) {
			t.Fatalf("superseded WorkspaceBinding error = %v, want %v", err, ErrCanonicalV19RepairNotCurrent)
		}
	})

	t.Run("terminal task", func(t *testing.T) {
		fixture := canonicalV19AttemptWriterFixture(t)
		canonicalV19RepairWriterExec(t, fixture.Home,
			`UPDATE task SET lifecycle='satisfied',terminal_at='2026-09-05T10:00:00Z' WHERE id='task-1'`)
		err := CreateCanonicalV19Repair(context.Background(), fixture.Home,
			canonicalV19RepairWriterInput("repair-task", CanonicalV19RepairTarget{TaskID: "task-1"}))
		if !errors.Is(err, ErrCanonicalV19RepairNotCurrent) {
			t.Fatalf("terminal Task error = %v, want %v", err, ErrCanonicalV19RepairNotCurrent)
		}
	})

	t.Run("terminal plan", func(t *testing.T) {
		fixture := canonicalV19AttemptWriterFixture(t)
		canonicalV19RepairWriterExec(t, fixture.Home,
			`UPDATE plan SET lifecycle='satisfied',terminal_at='2026-09-05T10:00:00Z' WHERE id='plan-root'`)
		err := CreateCanonicalV19Repair(context.Background(), fixture.Home,
			canonicalV19RepairWriterInput("repair-plan", CanonicalV19RepairTarget{PlanID: "plan-root"}))
		if !errors.Is(err, ErrCanonicalV19RepairNotCurrent) {
			t.Fatalf("terminal Plan error = %v, want %v", err, ErrCanonicalV19RepairNotCurrent)
		}
	})

	t.Run("terminal attempt", func(t *testing.T) {
		fixture := canonicalV19AttemptWriterFixture(t)
		if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home,
			canonicalV19AttemptWriterInput("attempt-1", "plan-root")); err != nil {
			t.Fatal(err)
		}
		canonicalV19AttemptWriterTerminalize(t, fixture.Home, "attempt-1", "failed", "2026-09-05T10:00:00Z")
		err := CreateCanonicalV19Repair(context.Background(), fixture.Home,
			canonicalV19RepairWriterInput("repair-attempt", CanonicalV19RepairTarget{AttemptID: "attempt-1"}))
		if !errors.Is(err, ErrCanonicalV19RepairNotCurrent) {
			t.Fatalf("terminal Attempt error = %v, want %v", err, ErrCanonicalV19RepairNotCurrent)
		}
	})
}

func TestResolveCanonicalV19RepairPersistsImmutableResolution(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	create := canonicalV19RepairWriterInput("repair-1", CanonicalV19RepairTarget{TaskID: "task-1"})
	if err := CreateCanonicalV19Repair(context.Background(), fixture.Home, create); err != nil {
		t.Fatal(err)
	}
	resolve := CanonicalV19RepairResolveInput{
		RepairID:       create.ID,
		Resolution:     "repaired",
		ResolvedAt:     "2026-09-05T10:01:00Z",
		EvidenceDigest: "repair-resolution-digest",
	}
	if err := ResolveCanonicalV19Repair(context.Background(), fixture.Home, resolve); err != nil {
		t.Fatal(err)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var resolution, resolvedAt, digest, actor string
	if err := db.sql.QueryRow(`SELECT resolution,resolved_at,evidence_digest,actor_ref
		FROM repair_resolution WHERE repair_id=?`, create.ID).Scan(&resolution, &resolvedAt, &digest, &actor); err != nil {
		t.Fatal(err)
	}
	if resolution != resolve.Resolution || resolvedAt != resolve.ResolvedAt || digest != resolve.EvidenceDigest || actor != "" {
		t.Fatalf("persisted Repair resolution = %q %q %q actor=%q", resolution, resolvedAt, digest, actor)
	}

	if err := ResolveCanonicalV19Repair(context.Background(), fixture.Home, resolve); !errors.Is(err, ErrCanonicalV19RepairNotCurrent) {
		t.Fatalf("replayed Repair resolution error = %v, want %v", err, ErrCanonicalV19RepairNotCurrent)
	}
}

func TestResolveCanonicalV19RepairRefusesStaleTarget(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home,
		canonicalV19AttemptWriterInput("attempt-1", "plan-root")); err != nil {
		t.Fatal(err)
	}
	create := canonicalV19RepairWriterInput("repair-attempt", CanonicalV19RepairTarget{AttemptID: "attempt-1"})
	if err := CreateCanonicalV19Repair(context.Background(), fixture.Home, create); err != nil {
		t.Fatal(err)
	}
	canonicalV19AttemptWriterTerminalize(t, fixture.Home, "attempt-1", "failed", "2026-09-05T10:02:00Z")

	err := ResolveCanonicalV19Repair(context.Background(), fixture.Home, CanonicalV19RepairResolveInput{
		RepairID: create.ID, Resolution: "superseded", ResolvedAt: "2026-09-05T10:03:00Z", EvidenceDigest: "stale-resolution-digest",
	})
	if !errors.Is(err, ErrCanonicalV19RepairNotCurrent) {
		t.Fatalf("stale Repair resolution error = %v, want %v", err, ErrCanonicalV19RepairNotCurrent)
	}
	if got := canonicalV19RepairWriterCount(t, fixture.Home, "repair_resolution"); got != 0 {
		t.Fatalf("Repair resolution rows after stale target = %d, want 0", got)
	}
}

func TestResolveCanonicalV19RepairOperatorAttestedRequiresActor(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	create := canonicalV19RepairWriterInput("repair-operator", CanonicalV19RepairTarget{ProjectID: "project-1"})
	if err := CreateCanonicalV19Repair(context.Background(), fixture.Home, create); err != nil {
		t.Fatal(err)
	}
	withoutActor := CanonicalV19RepairResolveInput{
		RepairID: create.ID, Resolution: "operator-attested", ResolvedAt: "2026-09-05T10:04:00Z", EvidenceDigest: "operator-evidence",
	}
	if err := ResolveCanonicalV19Repair(context.Background(), fixture.Home, withoutActor); err == nil {
		t.Fatal("operator-attested without actor_ref unexpectedly succeeded")
	}
	withActor := withoutActor
	withActor.ActorRef = "operator/local"
	if err := ResolveCanonicalV19Repair(context.Background(), fixture.Home, withActor); err != nil {
		t.Fatal(err)
	}
}

func TestCreateCanonicalV19RepairDuplicateIdentityDoesNotLeaveDanglingTarget(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	input := canonicalV19RepairWriterInput("repair-same", CanonicalV19RepairTarget{ProjectID: "project-1"})
	if err := CreateCanonicalV19Repair(context.Background(), fixture.Home, input); err != nil {
		t.Fatal(err)
	}
	if err := CreateCanonicalV19Repair(context.Background(), fixture.Home, input); !errors.Is(err, ErrCanonicalV19RepairConflict) {
		t.Fatalf("duplicate Repair error = %v, want %v", err, ErrCanonicalV19RepairConflict)
	}
	if got := canonicalV19RepairWriterCount(t, fixture.Home, "repair"); got != 1 {
		t.Fatalf("Repair rows after duplicate = %d, want 1", got)
	}
	if got := canonicalV19RepairWriterCount(t, fixture.Home, "repair_target"); got != 1 {
		t.Fatalf("Repair target rows after duplicate = %d, want 1", got)
	}
}

func TestCreateCanonicalV19RepairDoesNotCreateDatabase(t *testing.T) {
	home := t.TempDir()
	err := CreateCanonicalV19Repair(context.Background(), home,
		canonicalV19RepairWriterInput("repair-missing", CanonicalV19RepairTarget{ProjectID: "project-1"}))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing database error = %v, want os.ErrNotExist", err)
	}
	if _, statErr := os.Lstat(Path(home)); !os.IsNotExist(statErr) {
		t.Fatalf("missing database was created: stat error = %v", statErr)
	}
}

func canonicalV19RepairWriterInput(id string, target CanonicalV19RepairTarget) CanonicalV19RepairCreateInput {
	return CanonicalV19RepairCreateInput{
		ID:             id,
		RepairCode:     "canonical-test-repair",
		Reason:         "bounded canonical Repair regression evidence",
		EvidenceDigest: "repair-evidence-digest",
		CreatedAt:      "2026-09-05T10:00:00Z",
		Target:         target,
	}
}

func canonicalV19RepairWriterExec(t *testing.T, home, query string) {
	t.Helper()
	db, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
}

func canonicalV19RepairWriterCount(t *testing.T, home, table string) int {
	t.Helper()
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
