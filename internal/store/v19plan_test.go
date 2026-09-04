package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	db, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspace_binding(
		id,project_id,ordinal,repository_locator,repository_identity_digest,common_git_dir,physical_identity_digest,revision,established_at,superseded_at
	) VALUES('workspace-1','project-1',1,'projects/demo','repo-digest-1','projects/demo/.git','physical-digest-1',?,'2026-09-04T07:59:30Z','')`, storedRevision); err != nil {
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
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
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
