package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadCanonicalV19FleetSnapshotCoreLineage(t *testing.T) {
	fixture := canonicalV19AttemptWriterFixture(t)
	first := canonicalV19AttemptWriterInput("attempt-1", "plan-root")
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, first); err != nil {
		t.Fatal(err)
	}
	canonicalV19AttemptWriterTerminalize(t, fixture.Home, first.ID, "failed", "2026-09-04T09:01:00Z")
	second := canonicalV19AttemptWriterInput("attempt-2", "plan-root")
	second.CreatedAt = "2026-09-04T09:02:00Z"
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, second); err != nil {
		t.Fatal(err)
	}
	db, err := open(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO project(id,fleet_id,ordinal,display_name,created_at)
		VALUES('project-2','fleet-1',2,'second','2026-09-04T06:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateCanonicalV19Task(context.Background(), fixture.Home, CanonicalV19TaskCreateInput{
		ID: "task-2", ProjectID: "project-2", Goal: "second goal", GoalDigest: "second-digest", CreatedAt: "2026-09-04T08:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	db, err = open(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE project SET retired_at='2026-09-04T08:01:00Z' WHERE id='project-2'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadCanonicalV19FleetSnapshot(context.Background(), fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(Path(fixture.Home))
	if err != nil || string(after) != string(before) {
		t.Fatalf("snapshot changed database: %v", err)
	}
	if snapshot.Schema != "hand.fleet.core.v1" || snapshot.Completeness != "partial" || snapshot.FleetID != "fleet-1" {
		t.Fatalf("snapshot identity = %#v", snapshot)
	}
	if _, err := time.Parse(time.RFC3339Nano, snapshot.DBReadAt); err != nil {
		t.Fatalf("database read instant = %q: %v", snapshot.DBReadAt, err)
	}
	if len(snapshot.Projects) != 2 || snapshot.Projects[0].ID != "project-1" || snapshot.Projects[1].ID != "project-2" {
		t.Fatalf("project order = %#v", snapshot.Projects)
	}
	primary := snapshot.Projects[0]
	if primary.Workspace == nil || primary.Workspace.ID != "workspace-1" || primary.Policy == nil || primary.Policy.ID != "policy-1" {
		t.Fatalf("project current sources = %#v", primary)
	}
	if len(primary.Tasks) != 1 || primary.Tasks[0].ID != "task-1" || primary.Tasks[0].Plan == nil || primary.Tasks[0].Plan.ID != "plan-root" {
		t.Fatalf("primary lineage = %#v", primary.Tasks)
	}
	if attempt := primary.Tasks[0].Plan.Attempt; attempt == nil || attempt.ID != "attempt-2" || attempt.Ordinal != 2 || attempt.WorkerHarnessRef != second.WorkerHarnessRef {
		t.Fatalf("active successor = %#v", attempt)
	}
	secondary := snapshot.Projects[1]
	if secondary.RetiredAt == "" || secondary.Workspace != nil || secondary.Policy != nil || len(secondary.Tasks) != 1 || secondary.Tasks[0].Plan != nil {
		t.Fatalf("project without binding/Plan = %#v", secondary)
	}
}

func TestReadCanonicalV19FleetSnapshotRefusesMissingAndIncompatibleStores(t *testing.T) {
	missing := t.TempDir()
	if _, err := ReadCanonicalV19FleetSnapshot(context.Background(), missing); err == nil {
		t.Fatal("missing canonical database accepted")
	}
	if _, err := os.Stat(Path(missing)); !os.IsNotExist(err) {
		t.Fatalf("missing database created: %v", err)
	}
	legacy := t.TempDir()
	db, err := open(Path(legacy))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCanonicalV19FleetSnapshot(context.Background(), legacy); !errors.Is(err, ErrCanonicalV19SchemaMismatch) {
		t.Fatalf("legacy database = %v", err)
	}
	canonical := canonicalV19TaskWriterFixture(t, false)
	db, err = open(Path(canonical))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE stray(id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCanonicalV19FleetSnapshot(context.Background(), canonical); !errors.Is(err, ErrCanonicalV19SchemaMismatch) {
		t.Fatalf("drifted schema = %v", err)
	}
	if entries, err := os.ReadDir(filepath.Join(missing, "state")); !os.IsNotExist(err) || len(entries) != 0 {
		t.Fatalf("missing fleet state created: %v, %v", entries, err)
	}
}

func TestReadCanonicalV19FleetSnapshotSeparatesInputFromWakeAndHistoricalEffects(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	for _, input := range []CanonicalV19WorkerInputCreateInput{
		canonicalV19WorkerInputCreateInput(launch, "input-acked", "first instruction", "digest-first"),
		canonicalV19WorkerInputCreateInput(launch, "input-open", "second instruction", "digest-second"),
	} {
		if _, err := CreateCanonicalV19WorkerInput(context.Background(), fixture.Home, input); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := CreateCanonicalV19WorkerInputAcknowledgement(context.Background(), fixture.Home,
		canonicalV19WorkerInputAcknowledgementCreateInput("input-acked", launch.BindingID)); err != nil {
		t.Fatal(err)
	}
	wake, err := PrepareCanonicalV19WorkerWake(context.Background(), fixture.Home,
		canonicalV19WorkerWakePrepareInput(launch, "wake-open", 2))
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadCanonicalV19FleetSnapshot(context.Background(), fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(Path(fixture.Home))
	if err != nil || string(after) != string(before) {
		t.Fatalf("input snapshot mutated database: %v", err)
	}
	if len(snapshot.UnacknowledgedInputs) != 1 || snapshot.UnacknowledgedInputs[0].ID != "input-open" ||
		snapshot.UnacknowledgedInputs[0].Ordinal != 2 || snapshot.UnacknowledgedInputs[0].ExecutorBindingID != launch.BindingID {
		t.Fatalf("current unacknowledged input = %#v", snapshot.UnacknowledgedInputs)
	}
	if len(snapshot.UnresolvedOperations) != 1 || snapshot.UnresolvedOperations[0].ID != wake.OperationID ||
		snapshot.UnresolvedOperations[0].Kind != "worker-wake" || snapshot.UnresolvedOperations[0].State != "prepared" ||
		snapshot.UnresolvedOperations[0].SessionBindingID == "" || snapshot.UnresolvedOperations[0].ExecutorBindingID != launch.BindingID {
		t.Fatalf("independent wake operation = %#v", snapshot.UnresolvedOperations)
	}
	db, err := open(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO executor_binding_termination(
		executor_binding_id,terminal_kind,observed_at,evidence_digest
	) VALUES(?, 'provider-gone', '2026-09-09T05:20:00Z', 'executor-gone')`, launch.BindingID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err = ReadCanonicalV19FleetSnapshot(context.Background(), fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.UnacknowledgedInputs) != 0 || len(snapshot.UnresolvedOperations) != 1 ||
		snapshot.UnresolvedOperations[0].ID != wake.OperationID {
		t.Fatalf("historical input/wake separation = %#v", snapshot)
	}
}

func TestReadCanonicalV19FleetSnapshotCurrentExecutorUsesExactOpenBindings(t *testing.T) {
	fixture, session, launch := canonicalV19ExecutorBindingFixture(t)
	before, err := os.ReadFile(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadCanonicalV19FleetSnapshot(context.Background(), fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(Path(fixture.Home))
	if err != nil || string(after) != string(before) {
		t.Fatalf("resource snapshot mutated database: %v", err)
	}
	if snapshot.Completeness != "partial" || len(snapshot.CurrentOpenExecutorBindings) != 1 ||
		len(snapshot.CurrentOpenWorktreeBindings) != 1 || len(snapshot.CurrentOpenSessionBindings) != 1 {
		t.Fatalf("current open executor bindings = %#v", snapshot.CurrentOpenExecutorBindings)
	}
	current := snapshot.CurrentOpenExecutorBindings[0]
	if current.ProjectID != launch.ProjectID || current.TaskID != launch.TaskID ||
		current.PlanID != launch.PlanID || current.AttemptID != launch.AttemptID ||
		current.WorktreeBindingID != launch.WorktreeBindingID ||
		current.WorktreePath != filepath.Join(fixture.Home, "worktrees", launch.WorktreeBindingID) ||
		current.WorktreePhysicalIdentityDigest != "worktree-physical-1" ||
		current.SessionBindingID != session.BindingID || current.AdapterRef != session.AdapterRef ||
		current.ProviderSessionKey != "provider-session-1" ||
		current.ExecutorBindingID != launch.BindingID || current.ProviderExecutorKey != "provider-executor-1" {
		t.Fatalf("exact current resource chain = %#v", current)
	}
	db, err := open(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO executor_binding_termination(
		executor_binding_id,terminal_kind,observed_at,evidence_digest
	) VALUES(?, 'provider-gone', '2026-09-09T05:20:00Z', 'executor-gone')`, launch.BindingID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err = ReadCanonicalV19FleetSnapshot(context.Background(), fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.CurrentOpenExecutorBindings) != 0 {
		t.Fatalf("terminated executor still current: %#v", snapshot.CurrentOpenExecutorBindings)
	}
}

func TestReadCanonicalV19FleetSnapshotShowsOpenResourcesBeforeExecutor(t *testing.T) {
	fixture, worktree := canonicalV19WorktreeRemoveFixture(t)
	before, err := os.ReadFile(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadCanonicalV19FleetSnapshot(context.Background(), fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(Path(fixture.Home))
	if err != nil || string(after) != string(before) {
		t.Fatalf("worktree snapshot changed database: %v", err)
	}
	if snapshot.Completeness != "partial" || len(snapshot.CurrentOpenExecutorBindings) != 0 || len(snapshot.CurrentOpenSessionBindings) != 0 || len(snapshot.CurrentOpenWorktreeBindings) != 1 {
		t.Fatalf("open worktree before Session/Executor = %#v", snapshot)
	}
	binding := snapshot.CurrentOpenWorktreeBindings[0]
	if binding.ProjectID != worktree.ProjectID || binding.TaskID != worktree.TaskID ||
		binding.PlanID != worktree.PlanID || binding.AttemptID != worktree.AttemptID ||
		binding.ID != worktree.BindingID || binding.CreateOperationID != worktree.OperationID ||
		binding.Path != worktree.RequestedPath ||
		binding.CommonGitDir != worktree.ExpectedCommonGitDir ||
		binding.PrivateGitDir != filepath.Join("worktrees", worktree.BindingID, ".git-private") ||
		binding.LockReason != worktree.ExpectedLockReason || binding.BasisRevision != worktree.BasisRevision ||
		binding.HeadRevision != worktree.BasisRevision || binding.PhysicalIdentityDigest != "worktree-physical-1" ||
		binding.EstablishedAt != "2026-09-05T15:02:00Z" {
		t.Fatalf("exact open worktree = %#v", binding)
	}

	input := canonicalV19SessionAcquirePrepareInput(worktree, "operation-session-acquire-snapshot", "session-binding-snapshot")
	input.RequestedProviderSessionKey = "provider-session-snapshot"
	session, err := PrepareCanonicalV19SessionAcquire(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19SessionBinding(context.Background(), fixture.Home, CanonicalV19SessionBindingEvidence{
		OperationID: session.OperationID, ProviderSessionKey: input.RequestedProviderSessionKey,
		EstablishedAt: "2026-09-06T16:53:00Z", EvidenceDigest: "session-established-snapshot",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = ReadCanonicalV19FleetSnapshot(context.Background(), fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.CurrentOpenExecutorBindings) != 0 || len(snapshot.CurrentOpenWorktreeBindings) != 1 || len(snapshot.CurrentOpenSessionBindings) != 1 {
		t.Fatalf("open resources before Executor = %#v", snapshot)
	}
	current := snapshot.CurrentOpenSessionBindings[0]
	if current.ProjectID != session.ProjectID || current.TaskID != session.TaskID ||
		current.PlanID != session.PlanID || current.AttemptID != session.AttemptID ||
		current.WorktreeBindingID != worktree.BindingID || current.ID != session.BindingID ||
		current.Ordinal != 1 || current.AcquireOperationID != session.OperationID || current.AdapterRef != session.AdapterRef ||
		current.ProviderSessionKey != input.RequestedProviderSessionKey || current.EstablishedAt != "2026-09-06T16:53:00Z" {
		t.Fatalf("exact open session = %#v", current)
	}
}

func TestReadCanonicalV19FleetSnapshotRetiredProjectKeepsOpenResources(t *testing.T) {
	fixture, worktree, session := canonicalV19SessionBindingFixture(t)
	db, err := open(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE project SET retired_at='2026-09-09T05:20:00Z' WHERE id=?`, worktree.ProjectID); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadCanonicalV19FleetSnapshot(context.Background(), fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Projects) != 1 || snapshot.Projects[0].RetiredAt == "" ||
		len(snapshot.Projects[0].Tasks) != 1 || snapshot.Projects[0].Tasks[0].Plan == nil ||
		snapshot.Projects[0].Tasks[0].Plan.Attempt == nil ||
		snapshot.Projects[0].Tasks[0].Plan.Attempt.ID != worktree.AttemptID {
		t.Fatalf("retired Project active lineage = %#v", snapshot.Projects)
	}
	if len(snapshot.CurrentOpenWorktreeBindings) != 1 || snapshot.CurrentOpenWorktreeBindings[0].ID != worktree.BindingID ||
		len(snapshot.CurrentOpenSessionBindings) != 1 || snapshot.CurrentOpenSessionBindings[0].ID != session.BindingID ||
		len(snapshot.CurrentOpenExecutorBindings) != 0 {
		t.Fatalf("retired Project hid open resources before Executor = %#v", snapshot)
	}
}

func TestReadCanonicalV19FleetSnapshotOpenResourcesFollowReleases(t *testing.T) {
	fixture, worktree, session := canonicalV19SessionBindingFixture(t)
	request, err := PrepareCanonicalV19SessionRelease(context.Background(), fixture.Home, CanonicalV19SessionReleasePrepareInput{
		OperationID: "operation-session-release-snapshot", OperationKey: "operation-key-session-release-snapshot",
		SessionBindingID: session.BindingID, CreatedAt: "2026-09-06T16:54:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19SessionRelease(context.Background(), fixture.Home, CanonicalV19SessionReleasedEvidence{
		OperationID: request.OperationID, ReleasedAt: "2026-09-06T16:56:00Z", EvidenceDigest: "session-released-snapshot",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadCanonicalV19FleetSnapshot(context.Background(), fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.CurrentOpenWorktreeBindings) != 1 || len(snapshot.CurrentOpenSessionBindings) != 0 {
		t.Fatalf("released Session with open Worktree = %#v", snapshot)
	}
	remove := canonicalV19WorktreeRemovePrepareInput(worktree, "operation-worktree-remove-snapshot")
	if _, err := PrepareCanonicalV19WorktreeRemove(context.Background(), fixture.Home, remove); err != nil {
		t.Fatal(err)
	}
	if err := CompleteCanonicalV19WorktreeRemove(context.Background(), fixture.Home, CanonicalV19WorktreeRemovedEvidence{
		OperationID: remove.OperationID, RemovedAt: "2026-09-06T16:57:00Z", EvidenceDigest: "worktree-released-snapshot",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = ReadCanonicalV19FleetSnapshot(context.Background(), fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.CurrentOpenWorktreeBindings) != 0 || len(snapshot.CurrentOpenSessionBindings) != 0 {
		t.Fatalf("released Worktree still projected = %#v", snapshot)
	}
}

func TestReadCanonicalV19FleetSnapshotOpenResourcesDoNotRetargetSuccessor(t *testing.T) {
	fixture, worktree, session := canonicalV19SessionBindingFixture(t)
	canonicalV19AttemptWriterTerminalize(t, fixture.Home, worktree.AttemptID, "failed", "2026-09-09T05:20:00Z")
	successor := canonicalV19AttemptWriterInput("attempt-2", worktree.PlanID)
	successor.CreatedAt = "2026-09-09T05:21:00Z"
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, successor); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadCanonicalV19FleetSnapshot(context.Background(), fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.CurrentOpenWorktreeBindings) != 0 || len(snapshot.CurrentOpenSessionBindings) != 0 {
		t.Fatalf("historical open resources retargeted successor = %#v", snapshot)
	}
	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var open int
	if err := db.sql.QueryRow(`SELECT count(*) FROM session_binding s
		JOIN attempt_worktree_binding b ON b.id=s.worktree_binding_id
		WHERE s.id=? AND b.id=?
		AND NOT EXISTS (SELECT 1 FROM session_binding_release r WHERE r.session_binding_id=s.id)
		AND NOT EXISTS (SELECT 1 FROM worktree_binding_release r WHERE r.binding_id=b.id)`, session.BindingID, worktree.BindingID).Scan(&open); err != nil {
		t.Fatal(err)
	}
	if open != 1 {
		t.Fatalf("test lost historical open Session/Worktree: %d", open)
	}
}

func TestReadCanonicalV19FleetSnapshotExcludesHistoricalOpenExecutor(t *testing.T) {
	fixture, _, launch := canonicalV19ExecutorBindingFixture(t)
	canonicalV19AttemptWriterTerminalize(t, fixture.Home, launch.AttemptID, "failed", "2026-09-09T05:20:00Z")
	successor := canonicalV19AttemptWriterInput("attempt-2", launch.PlanID)
	successor.CreatedAt = "2026-09-09T05:21:00Z"
	if _, err := CreateCanonicalV19Attempt(context.Background(), fixture.Home, successor); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadCanonicalV19FleetSnapshot(context.Background(), fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Completeness != "partial" || len(snapshot.CurrentOpenExecutorBindings) != 0 {
		t.Fatalf("historical executor retargeted current projection: %#v", snapshot.CurrentOpenExecutorBindings)
	}
	if attempt := snapshot.Projects[0].Tasks[0].Plan.Attempt; attempt == nil || attempt.ID != successor.ID {
		t.Fatalf("successor lineage = %#v", attempt)
	}
	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var open int
	if err := db.sql.QueryRow(`SELECT count(*) FROM executor_binding e
		WHERE e.id=? AND NOT EXISTS (SELECT 1 FROM executor_binding_termination x WHERE x.executor_binding_id=e.id)`,
		launch.BindingID).Scan(&open); err != nil {
		t.Fatal(err)
	}
	if open != 1 {
		t.Fatalf("test lost historical open executor: %d", open)
	}
}

func TestCanonicalV19SnapshotCurrentExecutorsQueryUsesActiveIndexes(t *testing.T) {
	fixture, _, _ := canonicalV19ExecutorBindingFixture(t)
	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.sql.Query("EXPLAIN QUERY PLAN " + canonicalV19SnapshotCurrentExecutorBindingsQuery)
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
	for _, index := range []string{"task_active_by_project", "plan_active_by_task", "attempt_active_by_plan", "executor_binding_attempt"} {
		if !strings.Contains(plan.String(), index) {
			t.Fatalf("current resource query missed %s:\n%s", index, plan.String())
		}
	}
	if strings.Contains(plan.String(), "SCAN p ") || strings.Contains(plan.String(), "SCAN a ") || strings.Contains(plan.String(), "SCAN e ") {
		t.Fatalf("current resource query scans historical binding or lineage rows:\n%s", plan.String())
	}
}

func TestCanonicalV19SnapshotCurrentResourcesQueriesUseActiveIndexes(t *testing.T) {
	fixture, _, _ := canonicalV19SessionBindingFixture(t)
	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, query := range []struct {
		name    string
		sql     string
		indexes []string
	}{
		{"worktree", canonicalV19SnapshotCurrentWorktreeBindingsQuery, []string{
			"task_active_by_project", "plan_active_by_task", "attempt_active_by_plan",
			"sqlite_autoindex_attempt_worktree_binding_", "sqlite_autoindex_worktree_binding_release_",
		}},
		{"session", canonicalV19SnapshotCurrentSessionBindingsQuery, []string{
			"task_active_by_project", "plan_active_by_task", "attempt_active_by_plan",
			"sqlite_autoindex_attempt_worktree_binding_", "session_binding_attempt_history",
			"sqlite_autoindex_worktree_binding_release_", "sqlite_autoindex_session_binding_release_",
		}},
	} {
		t.Run(query.name, func(t *testing.T) {
			rows, err := db.sql.Query("EXPLAIN QUERY PLAN " + query.sql)
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
			for _, index := range query.indexes {
				if !strings.Contains(plan.String(), index) {
					t.Fatalf("current %s query missed %s:\n%s", query.name, index, plan.String())
				}
			}
			if strings.Contains(plan.String(), "SCAN p ") || strings.Contains(plan.String(), "SCAN a ") ||
				strings.Contains(plan.String(), "SCAN b ") || strings.Contains(plan.String(), "SCAN s ") {
				t.Fatalf("current %s query scans historical lineage/resource rows:\n%s", query.name, plan.String())
			}
		})
	}
}
