package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDecisionDeliverCLIRecordsExactAnswerInputWithoutWake(t *testing.T) {
	home, launch := canonicalV19DecisionDeliveryCLIFixture(t)
	decision := canonicalV19DecisionTestInput("attempt")
	if err := CreateCanonicalV19Decision(context.Background(), home, decision); err != nil {
		t.Fatal(err)
	}
	answer := canonicalV19DecisionTestAnswer(decision.ID)
	name := "hand"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	build := exec.Command("go", "build", "-tags=test", "-o", bin, ".")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Hand: %v: %s", err, out)
	}
	infra := t.TempDir()
	run := func(args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = infra
		cmd.Env = append(os.Environ(), "HAND_HOME="+home, "SECONDHAND_HOME="+infra)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	args := []string{"decision", "deliver", decision.ID,
		"--answer-id", answer.ID, "--input-id", "input-exact-answer",
		"--answer-origin-id", "origin-exact-answer", "--attempt-id", launch.AttemptID,
		"--executor-binding-id", launch.BindingID, "--created-at", "2026-09-21T09:03:00Z"}
	if out, err := run(args...); err == nil {
		t.Fatalf("deliver unanswered Decision accepted: %q", out)
	}
	canonicalV19DecisionAssertCount(t, home, "SELECT count(*) FROM worker_input", 0)
	if err := CreateCanonicalV19DecisionAnswer(context.Background(), home, answer); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		out, err := run(args...)
		if err != nil || !strings.Contains(out, "input-exact-answer") || strings.Contains(out, answer.Answer) {
			t.Fatalf("deliver exact Answer = %q, %v", out, err)
		}
	}
	canonicalV19DecisionAssertCount(t, home, "SELECT count(*) FROM worker_input_answer_origin", 1)
	canonicalV19DecisionAssertCount(t, home, "SELECT count(*) FROM worker_input", 1)
	canonicalV19DecisionAssertCount(t, home, "SELECT count(*) FROM worker_wake_operation", 0)
	canonicalV19DecisionAssertCount(t, home, "SELECT count(*) FROM worker_input_acknowledgement", 0)
	shown, err := run("fleet", "snapshot")
	if err != nil || !strings.Contains(shown, "unacknowledged_inputs[1]") ||
		!strings.Contains(shown, "input-exact-answer,"+launch.AttemptID+","+launch.BindingID+",1,answer,") ||
		!strings.Contains(shown, "attention: unknown") {
		t.Fatalf("unacknowledged Answer input without Wake = %q, %v", shown, err)
	}
	db, err := openReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	var payload []byte
	var digest string
	err = db.sql.QueryRow("SELECT payload,payload_digest FROM worker_input WHERE id=?", "input-exact-answer").Scan(&payload, &digest)
	_ = db.Close()
	if err != nil || string(payload) != answer.Answer || digest != answer.AnswerDigest {
		t.Fatalf("exact Answer payload = %q, %q, %v", payload, digest, err)
	}
	wrong := append([]string(nil), args...)
	wrong[4] = "another-answer"
	if out, err := run(wrong...); err == nil {
		t.Fatalf("deliver changed Answer ID accepted: %q", out)
	}
	duplicate := append([]string(nil), args...)
	duplicate[6], duplicate[8] = "input-duplicate", "origin-duplicate"
	if out, err := run(duplicate...); err == nil {
		t.Fatalf("deliver second Answer-origin input accepted: %q", out)
	}
	canonicalV19DecisionTestStopExecutor(t, home, launch)
	if out, err := run(duplicate...); err == nil {
		t.Fatalf("deliver to stopped Executor accepted: %q", out)
	}
	if out, err := run(args...); err != nil {
		t.Fatalf("replay after Executor stopped: %q, %v", out, err)
	}
	successor := canonicalV19DecisionTestRetryExecutor(t, home, launch.AttemptID)
	late := append([]string(nil), duplicate...)
	late[10], late[12] = successor.AttemptID, successor.BindingID
	if out, err := run(late...); err == nil {
		t.Fatalf("deliver old Decision to successor accepted: %q", out)
	}
	canonicalV19DecisionAssertCount(t, home, "SELECT count(*) FROM worker_input", 1)
	canonicalV19DecisionAssertCount(t, home, "SELECT count(*) FROM worker_input WHERE attempt_id='attempt-2'", 0)
	shown, err = run("fleet", "snapshot")
	if err != nil || !strings.Contains(shown, "unacknowledged_inputs[0]") || strings.Contains(shown, "input-exact-answer") {
		t.Fatalf("historical Answer input retargeted successor = %q, %v", shown, err)
	}
}

func canonicalV19DecisionDeliveryCLIFixture(t *testing.T) (string, CanonicalV19LaunchRequest) {
	t.Helper()
	ctx := context.Background()
	home := filepath.Join(t.TempDir(), "fleet")
	if _, err := InitializeCanonicalV19(ctx, home); err != nil {
		t.Fatal(err)
	}
	repository := filepath.Join(home, "projects", "demo")
	if err := os.Mkdir(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	canonicalV19PlanWriterGit(t, repository, "init", "-q")
	canonicalV19PlanWriterGit(t, repository, "config", "user.name", "decision-cli-test")
	canonicalV19PlanWriterGit(t, repository, "config", "user.email", "decision-cli-test@example.invalid")
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("decision delivery fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	canonicalV19PlanWriterGit(t, repository, "add", "README.md")
	canonicalV19PlanWriterGit(t, repository, "commit", "-q", "-m", "fixture")
	projectID, workspaceID, err := RegisterCanonicalV19Project(ctx, home, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateCanonicalV19Task(ctx, home, CanonicalV19TaskCreateInput{
		ID: "task-1", ProjectID: projectID, Goal: "deliver exact Answer",
		GoalDigest: "goal-digest", CreatedAt: "2026-09-04T07:59:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RecordCanonicalV19Policy(ctx, home, CanonicalV19PolicyRecordInput{
		ID: "policy-1", ProjectID: projectID,
	}); err != nil {
		t.Fatal(err)
	}
	plan := canonicalV19PlanWriterInput("plan-root")
	plan.WorkspaceBindingID = workspaceID
	if _, err := CreateCanonicalV19RootPlan(ctx, home, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateCanonicalV19Attempt(ctx, home, canonicalV19AttemptWriterInput("attempt-1", plan.ID)); err != nil {
		t.Fatal(err)
	}
	worktree, err := PrepareCanonicalV19WorktreeCreate(ctx, home,
		canonicalV19WorktreeCreatePrepareInput(home, "operation-create", "binding-1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19WorktreeBinding(ctx, home,
		canonicalV19WorktreeBindingEvidence(worktree, "worktree-physical-1")); err != nil {
		t.Fatal(err)
	}
	sessionInput := canonicalV19SessionAcquirePrepareInput(worktree, "operation-session-acquire", "session-binding-1")
	sessionInput.RequestedProviderSessionKey = "provider-session-1"
	session, err := PrepareCanonicalV19SessionAcquire(ctx, home, sessionInput)
	if err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19SessionBinding(ctx, home, CanonicalV19SessionBindingEvidence{
		OperationID: session.OperationID, ProviderSessionKey: "provider-session-1",
		EstablishedAt: "2026-09-06T16:53:00Z", EvidenceDigest: "session-established",
	}); err != nil {
		t.Fatal(err)
	}
	launch, err := PrepareCanonicalV19Launch(ctx, home,
		canonicalV19LaunchPrepareInput(worktree, session, "operation-launch", "executor-binding-1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := EstablishCanonicalV19ExecutorBinding(ctx, home, CanonicalV19ExecutorBindingEvidence{
		OperationID: launch.OperationID, ProviderExecutorKey: "provider-executor-1",
		EstablishedAt: "2026-09-06T17:04:00Z", EvidenceDigest: "executor-established",
	}); err != nil {
		t.Fatal(err)
	}
	return home, launch
}
