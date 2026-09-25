package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCreateCanonicalV19RootPlanPersistsExactCapturedMeaning(t *testing.T) {
	fixture := canonicalV19PlanWriterFixture(t, "")
	input := canonicalV19PlanWriterInput("plan-root")
	ordinal, err := CreateCanonicalV19RootPlan(context.Background(), fixture.Home, input)
	if err != nil {
		t.Fatal(err)
	}
	if ordinal != 1 {
		t.Fatalf("root Plan ordinal = %d, want 1", ordinal)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var got CanonicalV19PlanCreateInput
	var gotOrdinal int64
	var lineage, lifecycle, terminalAt string
	var predecessor sql.NullString
	if err := db.sql.QueryRow(`SELECT id,task_id,ordinal,lineage_kind,predecessor_plan_id,intent,judgment,basis,brief,
		brief_digest,workspace_binding_id,policy_revision_id,lifecycle,created_at,terminal_at
		FROM plan WHERE id=?`, input.ID).Scan(
		&got.ID, &got.TaskID, &gotOrdinal, &lineage, &predecessor, &got.Intent, &got.Judgment,
		&got.Basis, &got.Brief, &got.BriefDigest, &got.WorkspaceBindingID, &got.PolicyRevisionID,
		&lifecycle, &got.CreatedAt, &terminalAt,
	); err != nil {
		t.Fatal(err)
	}
	if got != input || gotOrdinal != 1 || lineage != "root" || predecessor.Valid || lifecycle != "active" || terminalAt != "" {
		t.Fatalf("persisted root Plan = %#v ordinal=%d lineage=%q predecessor=%#v lifecycle=%q terminal_at=%q",
			got, gotOrdinal, lineage, predecessor, lifecycle, terminalAt)
	}
}

func TestCreateCanonicalV19RootPlanRefusesSecondRoot(t *testing.T) {
	fixture := canonicalV19PlanWriterFixture(t, "")
	first := canonicalV19PlanWriterInput("plan-root")
	if _, err := CreateCanonicalV19RootPlan(context.Background(), fixture.Home, first); err != nil {
		t.Fatal(err)
	}
	second := canonicalV19PlanWriterInput("plan-second-root")
	if _, err := CreateCanonicalV19RootPlan(context.Background(), fixture.Home, second); !errors.Is(err, ErrCanonicalV19PlanConflict) {
		t.Fatalf("second root error = %v, want %v", err, ErrCanonicalV19PlanConflict)
	}
	if got := canonicalV19PlanWriterCount(t, fixture.Home); got != 1 {
		t.Fatalf("Plan rows after second-root refusal = %d, want 1", got)
	}
}

func TestReplanCanonicalV19PlanSupersedesOnlyExactPredecessor(t *testing.T) {
	fixture := canonicalV19PlanWriterFixture(t, "")
	root := canonicalV19PlanWriterInput("plan-root")
	if _, err := CreateCanonicalV19RootPlan(context.Background(), fixture.Home, root); err != nil {
		t.Fatal(err)
	}
	successor := canonicalV19PlanWriterInput("plan-replan")
	successor.Intent = "execute"
	successor.Judgment = "substantial"
	successor.Basis = "root exploration established the implementation basis"
	successor.Brief = "implement the bounded successor"
	successor.BriefDigest = "brief-digest-replan"
	successor.CreatedAt = "2026-09-04T08:01:00Z"
	ordinal, err := ReplanCanonicalV19Plan(context.Background(), fixture.Home, CanonicalV19PlanReplanInput{
		PredecessorPlanID: root.ID,
		Successor:         successor,
		SupersededAt:      "2026-09-04T08:00:59Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ordinal != 2 {
		t.Fatalf("replan ordinal = %d, want 2", ordinal)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var rootLifecycle, rootTerminal string
	if err := db.sql.QueryRow(`SELECT lifecycle,terminal_at FROM plan WHERE id=?`, root.ID).Scan(&rootLifecycle, &rootTerminal); err != nil {
		t.Fatal(err)
	}
	if rootLifecycle != "superseded" || rootTerminal != "2026-09-04T08:00:59Z" {
		t.Fatalf("root after replan = lifecycle %q terminal_at %q", rootLifecycle, rootTerminal)
	}
	var predecessor, lineage, lifecycle, terminalAt string
	var gotOrdinal int64
	if err := db.sql.QueryRow(`SELECT ordinal,lineage_kind,predecessor_plan_id,lifecycle,terminal_at FROM plan WHERE id=?`, successor.ID).
		Scan(&gotOrdinal, &lineage, &predecessor, &lifecycle, &terminalAt); err != nil {
		t.Fatal(err)
	}
	if gotOrdinal != 2 || lineage != "replan" || predecessor != root.ID || lifecycle != "active" || terminalAt != "" {
		t.Fatalf("successor = ordinal=%d lineage=%q predecessor=%q lifecycle=%q terminal_at=%q",
			gotOrdinal, lineage, predecessor, lifecycle, terminalAt)
	}

	stale := canonicalV19PlanWriterInput("plan-stale")
	_, err = ReplanCanonicalV19Plan(context.Background(), fixture.Home, CanonicalV19PlanReplanInput{
		PredecessorPlanID: root.ID,
		Successor:         stale,
		SupersededAt:      "2026-09-04T08:02:00Z",
	})
	if !errors.Is(err, ErrCanonicalV19PlanNotCurrent) {
		t.Fatalf("stale replan error = %v, want %v", err, ErrCanonicalV19PlanNotCurrent)
	}
	if got := canonicalV19PlanWriterCount(t, fixture.Home); got != 2 {
		t.Fatalf("Plan rows after stale replan = %d, want 2", got)
	}
}

func TestReplanCanonicalV19PlanConcurrentSuccessorsHaveOneWinner(t *testing.T) {
	fixture := canonicalV19PlanWriterFixture(t, "")
	if _, err := CreateCanonicalV19RootPlan(context.Background(), fixture.Home, canonicalV19PlanWriterInput("plan-root")); err != nil {
		t.Fatal(err)
	}
	replan := func(id string) func() error {
		return func() error {
			successor := canonicalV19PlanWriterInput(id)
			successor.CreatedAt = "2026-09-04T08:01:00Z"
			_, err := ReplanCanonicalV19Plan(context.Background(), fixture.Home, CanonicalV19PlanReplanInput{
				PredecessorPlanID: "plan-root", Successor: successor, SupersededAt: "2026-09-04T08:00:59Z",
			})
			return err
		}
	}
	wins := 0
	for _, err := range canonicalV19RaceWriters(replan("plan-a"), replan("plan-b")) {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrCanonicalV19PlanNotCurrent) {
			t.Fatalf("concurrent replan = %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent replan winners = %d, want 1", wins)
	}
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM plan`, 2)
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM plan WHERE id='plan-root' AND lifecycle='superseded'`, 1)
	canonicalV19DecisionAssertCount(t, fixture.Home, `SELECT count(*) FROM plan
		WHERE ordinal=2 AND lineage_kind='replan' AND predecessor_plan_id='plan-root' AND lifecycle='active'`, 1)
}

func TestReplanCanonicalV19PlanRollsBackPredecessorWhenSuccessorConflicts(t *testing.T) {
	fixture := canonicalV19PlanWriterFixture(t, "")
	root := canonicalV19PlanWriterInput("plan-root")
	if _, err := CreateCanonicalV19RootPlan(context.Background(), fixture.Home, root); err != nil {
		t.Fatal(err)
	}
	conflicting := canonicalV19PlanWriterInput(root.ID)
	_, err := ReplanCanonicalV19Plan(context.Background(), fixture.Home, CanonicalV19PlanReplanInput{
		PredecessorPlanID: root.ID,
		Successor:         conflicting,
		SupersededAt:      "2026-09-04T08:03:00Z",
	})
	if !errors.Is(err, ErrCanonicalV19PlanConflict) {
		t.Fatalf("conflicting successor error = %v, want %v", err, ErrCanonicalV19PlanConflict)
	}

	db, err := openReadOnly(fixture.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var lifecycle, terminalAt string
	if err := db.sql.QueryRow(`SELECT lifecycle,terminal_at FROM plan WHERE id=?`, root.ID).Scan(&lifecycle, &terminalAt); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "active" || terminalAt != "" {
		t.Fatalf("predecessor leaked partial supersede: lifecycle=%q terminal_at=%q", lifecycle, terminalAt)
	}
	if got := canonicalV19PlanWriterCount(t, fixture.Home); got != 1 {
		t.Fatalf("Plan rows after rolled-back replan = %d, want 1", got)
	}
}

func TestCanonicalV19PlanWriterRefusesStaleExactBindingAndPolicyWithoutRetarget(t *testing.T) {
	fixture := canonicalV19PlanWriterFixture(t, "")
	db, err := open(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE workspace_binding SET superseded_at='2026-09-04T08:04:00Z' WHERE id='workspace-1'`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspace_binding(
		id,project_id,ordinal,repository_locator,repository_identity_digest,common_git_dir,physical_identity_digest,revision,established_at,superseded_at
	) VALUES('workspace-2','project-1',2,'projects/demo','repo-digest-2','projects/demo/.git','physical-digest-2',?,'2026-09-04T08:04:00Z','')`, fixture.Revision); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE policy_revision SET superseded_at='2026-09-04T08:04:00Z' WHERE id='policy-1'`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO policy_revision(
		id,project_id,ordinal,policy_digest,created_at,superseded_at
	) VALUES('policy-2','project-1',2,'policy-digest-2','2026-09-04T08:04:00Z','')`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	input := canonicalV19PlanWriterInput("plan-stale-basis")
	_, err = CreateCanonicalV19RootPlan(context.Background(), fixture.Home, input)
	if !errors.Is(err, ErrCanonicalV19PlanNotCurrent) {
		t.Fatalf("stale exact binding/policy error = %v, want %v", err, ErrCanonicalV19PlanNotCurrent)
	}
	if got := canonicalV19PlanWriterCount(t, fixture.Home); got != 0 {
		t.Fatalf("Plan rows after stale basis refusal = %d, want 0", got)
	}
}

func TestCanonicalV19PlanWriterRequiresPositiveExactGitRevision(t *testing.T) {
	fixture := canonicalV19PlanWriterFixture(t, strings.Repeat("f", 40))
	_, err := CreateCanonicalV19RootPlan(context.Background(), fixture.Home, canonicalV19PlanWriterInput("plan-bad-revision"))
	if !errors.Is(err, ErrCanonicalV19PlanGitBasis) {
		t.Fatalf("missing exact Git revision error = %v, want %v", err, ErrCanonicalV19PlanGitBasis)
	}
	if got := canonicalV19PlanWriterCount(t, fixture.Home); got != 0 {
		t.Fatalf("Plan rows after Git-basis refusal = %d, want 0", got)
	}
}

func TestCanonicalV19PlanWriterRefusesTerminalTask(t *testing.T) {
	fixture := canonicalV19PlanWriterFixture(t, "")
	db, err := open(Path(fixture.Home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE task SET lifecycle='satisfied', terminal_at='2026-09-04T08:05:00Z' WHERE id='task-1'`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = CreateCanonicalV19RootPlan(context.Background(), fixture.Home, canonicalV19PlanWriterInput("plan-terminal-task"))
	if !errors.Is(err, ErrCanonicalV19PlanNotCurrent) {
		t.Fatalf("terminal Task error = %v, want %v", err, ErrCanonicalV19PlanNotCurrent)
	}
}

type canonicalV19PlanWriterTestFixture struct {
	Home     string
	Revision string
}

func canonicalV19PlanWriterFixture(t *testing.T, storedRevision string) canonicalV19PlanWriterTestFixture {
	t.Helper()
	home := canonicalV19TaskWriterFixture(t, false)
	if _, err := CreateCanonicalV19Task(context.Background(), home, CanonicalV19TaskCreateInput{
		ID: "task-1", ProjectID: "project-1", Goal: "ship canonical Plan semantics", GoalDigest: "goal-digest", CreatedAt: "2026-09-04T07:59:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	repository := filepath.Join(home, "projects", "demo")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	canonicalV19PlanWriterGit(t, repository, "init", "-q")
	canonicalV19PlanWriterGit(t, repository, "config", "user.name", "plan-writer-test")
	canonicalV19PlanWriterGit(t, repository, "config", "user.email", "plan-writer-test@example.invalid")
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("canonical plan fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	canonicalV19PlanWriterGit(t, repository, "add", "README.md")
	canonicalV19PlanWriterGit(t, repository, "commit", "-q", "-m", "fixture")
	revision := strings.TrimSpace(canonicalV19PlanWriterGit(t, repository, "rev-parse", "HEAD"))
	if storedRevision == "" {
		storedRevision = revision
	}
	observed, err := observeCanonicalV19Project(home, "demo")
	if err != nil {
		t.Fatal(err)
	}

	db, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspace_binding(
		id,project_id,ordinal,repository_locator,repository_identity_digest,common_git_dir,physical_identity_digest,revision,established_at,superseded_at
	) VALUES('workspace-1','project-1',1,'projects/demo',?,'projects/demo/.git',?,?,'2026-09-04T07:59:30Z','')`,
		observed.RepositoryIdentity, observed.PhysicalIdentity, storedRevision); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO policy_revision(
		id,project_id,ordinal,policy_digest,created_at,superseded_at
	) VALUES('policy-1','project-1',1,'policy-digest-1','2026-09-04T07:59:30Z','')`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return canonicalV19PlanWriterTestFixture{Home: home, Revision: revision}
}

func canonicalV19PlanWriterInput(id string) CanonicalV19PlanCreateInput {
	return CanonicalV19PlanCreateInput{
		ID:                 id,
		TaskID:             "task-1",
		Intent:             "explore",
		Judgment:           "bounded",
		Basis:              "operator goal plus exact repository evidence",
		Brief:              "establish a bounded canonical Plan",
		BriefDigest:        "brief-digest-root",
		WorkspaceBindingID: "workspace-1",
		PolicyRevisionID:   "policy-1",
		CreatedAt:          "2026-09-04T08:00:00Z",
	}
}

func canonicalV19PlanWriterGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-c", "gc.autoDetach=false", "-c", "maintenance.autoDetach=false", "-C", dir}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return string(output)
}

func canonicalV19PlanWriterCount(t *testing.T, home string) int {
	t.Helper()
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM plan`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestAbandonCanonicalV19PlanTerminalizesExactPlanWithoutSuccessor(t *testing.T) {
	home := canonicalV19AttemptWriterFixture(t).Home
	ctx := context.Background()
	if err := AbandonCanonicalV19Plan(ctx, home, "plan-root", "2026-09-04T09:01:00Z"); err != nil {
		t.Fatal(err)
	}
	if got := canonicalV19Lifecycle(t, home, "plan", "plan-root"); got != "abandoned/2026-09-04T09:01:00Z" {
		t.Fatalf("Plan lifecycle = %q", got)
	}
	if err := AbandonCanonicalV19Plan(ctx, home, "plan-root", "2026-09-04T09:02:00Z"); !errors.Is(err, ErrCanonicalV19PlanNotCurrent) {
		t.Fatalf("replayed abandon = %v, want %v", err, ErrCanonicalV19PlanNotCurrent)
	}
	if _, err := CreateCanonicalV19Attempt(ctx, home, canonicalV19AttemptWriterInput("attempt-1", "plan-root")); !errors.Is(err, ErrCanonicalV19AttemptNotCurrent) {
		t.Fatalf("retry under abandoned Plan = %v, want %v", err, ErrCanonicalV19AttemptNotCurrent)
	}
	if _, err := CreateCanonicalV19RootPlan(ctx, home, canonicalV19PlanWriterInput("plan-next")); !errors.Is(err, ErrCanonicalV19PlanConflict) {
		t.Fatalf("root Plan after abandon = %v, want %v", err, ErrCanonicalV19PlanConflict)
	}
	if got := canonicalV19Lifecycle(t, home, "plan", "plan-root"); got != "abandoned/2026-09-04T09:01:00Z" {
		t.Fatalf("Plan lifecycle after stale writers = %q", got)
	}
	if got := canonicalV19Lifecycle(t, home, "task", "task-1"); got != "active/" {
		t.Fatalf("Task lifecycle = %q, want active", got)
	}
}

func TestAbandonCanonicalV19PlanRefusesOpenObligationsWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		seed       func(*testing.T) string
	}{
		{"ActiveAttempt", "active Attempt", func(t *testing.T) string {
			home := canonicalV19AttemptWriterFixture(t).Home
			if _, err := CreateCanonicalV19Attempt(context.Background(), home, canonicalV19AttemptWriterInput("attempt-1", "plan-root")); err != nil {
				t.Fatal(err)
			}
			return home
		}},
		{"UnresolvedExternalOperation", "unresolved external operation", func(t *testing.T) string {
			home := canonicalV19AttemptWriterFixture(t).Home
			canonicalV19RepairWriterExec(t, home, `INSERT INTO external_operation(
				id,kind,adapter_ref,operation_key,request_digest,project_id,task_id,plan_id,
				primary_scope_kind,primary_scope_key,created_at,state_changed_at
			) VALUES('operation-1','qualification','adapter','key','digest','project-1','task-1','plan-root',
				'qualification','plan-root','2026-09-04T09:00:30Z','2026-09-04T09:00:30Z')`)
			return home
		}},
		{"OpenExecutorBinding", "open ExecutorBinding", func(t *testing.T) string {
			fixture, _, _ := canonicalV19ExecutorBindingFixture(t)
			canonicalV19RepairWriterExec(t, fixture.Home, `UPDATE attempt SET lifecycle='failed',terminal_at='2026-09-06T17:05:00Z' WHERE id='attempt-1'`)
			return fixture.Home
		}},
		{"OpenRepair", "open Repair", func(t *testing.T) string {
			home := canonicalV19AttemptWriterFixture(t).Home
			canonicalV19RepairWriterExec(t, home, `BEGIN IMMEDIATE;
				INSERT INTO repair_target(repair_id,plan_id) VALUES('repair-1','plan-root');
				INSERT INTO repair(id,repair_code,reason,evidence_digest,created_at)
				VALUES('repair-1','plan-check','inspect','digest','2026-09-04T09:00:30Z');
				COMMIT`)
			return home
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := tc.seed(t)
			err := AbandonCanonicalV19Plan(context.Background(), home, "plan-root", "2026-09-07T00:00:00Z")
			if !errors.Is(err, ErrCanonicalV19PlanNotCurrent) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("abandon with %s = %v, want %v naming %q", tc.name, err, ErrCanonicalV19PlanNotCurrent, tc.want)
			}
			if got := canonicalV19Lifecycle(t, home, "plan", "plan-root"); got != "active/" {
				t.Fatalf("Plan lifecycle = %q, want active", got)
			}
		})
	}
}

func TestAbandonCanonicalV19PlanRacesSuccessorWritersWithOneWinner(t *testing.T) {
	for _, tc := range []struct {
		name      string
		loserErr  error
		successor func(string) error
	}{
		{"Retry", ErrCanonicalV19AttemptNotCurrent, func(home string) error {
			_, err := CreateCanonicalV19Attempt(context.Background(), home, canonicalV19AttemptWriterInput("attempt-1", "plan-root"))
			return err
		}},
		{"Replan", ErrCanonicalV19PlanNotCurrent, func(home string) error {
			successor := canonicalV19PlanWriterInput("plan-replan")
			_, err := ReplanCanonicalV19Plan(context.Background(), home, CanonicalV19PlanReplanInput{
				PredecessorPlanID: "plan-root", Successor: successor, SupersededAt: "2026-09-04T09:01:00Z",
			})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := canonicalV19AttemptWriterFixture(t).Home
			start := make(chan struct{})
			var abandonErr, successorErr error
			var workers sync.WaitGroup
			workers.Go(func() {
				<-start
				abandonErr = AbandonCanonicalV19Plan(context.Background(), home, "plan-root", "2026-09-04T09:01:00Z")
			})
			workers.Go(func() {
				<-start
				successorErr = tc.successor(home)
			})
			close(start)
			workers.Wait()
			lifecycle := canonicalV19Lifecycle(t, home, "plan", "plan-root")
			switch {
			case abandonErr == nil && errors.Is(successorErr, tc.loserErr):
				if lifecycle != "abandoned/2026-09-04T09:01:00Z" || canonicalV19AttemptWriterCount(t, home) != 0 || canonicalV19PlanWriterCount(t, home) != 1 {
					t.Fatalf("abandon winner left Plan %q with successor history", lifecycle)
				}
			case successorErr == nil && errors.Is(abandonErr, ErrCanonicalV19PlanNotCurrent):
				if lifecycle == "abandoned/2026-09-04T09:01:00Z" {
					t.Fatalf("successor winner still abandoned Plan")
				}
			default:
				t.Fatalf("abandon/%s = %v/%v, want one winner and one typed loser", tc.name, abandonErr, successorErr)
			}
		})
	}
}

func canonicalV19Lifecycle(t *testing.T, home, table, id string) string {
	t.Helper()
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var lifecycle string
	if err := db.sql.QueryRow(`SELECT lifecycle||'/'||terminal_at FROM `+table+` WHERE id=?`, id).Scan(&lifecycle); err != nil {
		t.Fatal(err)
	}
	return lifecycle
}
