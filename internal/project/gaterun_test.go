package project

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/faketool"
)

func TestGateRunPRsCollectsCompletedRunPRs(t *testing.T) {
	fakeNoMistakes(t, "  completed    97-gate-visibility   758d72bf  2026-08-03 04:29  https://github.com/atqamz/hand/pull/120\n"+
		"  running      74-workspace-leak    b2f584f9  2026-08-03 04:20\n")

	prs, err := GateRunPRs(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !prs["https://github.com/atqamz/hand/pull/120"] {
		t.Fatal("expected a completed run's own recorded PR URL to be collected")
	}
	if prs["https://github.com/atqamz/hand/pull/999"] {
		t.Fatal("expected no entry for a PR no completed run recorded")
	}
}

// Covers a run that recorded a PR URL but never reached completed - running or failed both leave
// the gate un-cleared for that commit, so neither should count as evidence a run happened.
func TestGateRunPRsIgnoresNonCompletedRuns(t *testing.T) {
	fakeNoMistakes(t, "  failed       97-gate-visibility   758d72bf  2026-08-03 04:29  https://github.com/atqamz/hand/pull/120\n")

	prs, err := GateRunPRs(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if prs["https://github.com/atqamz/hand/pull/120"] {
		t.Fatal("a failed run must not count as gate coverage even though it recorded the PR URL")
	}
}

func TestGateRunPRsMissingClonePath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	prs, err := GateRunPRs(context.Background(), missing)
	if err == nil {
		t.Fatal("expected error for a clone path that does not exist")
	}
	if prs != nil {
		t.Fatal("a missing clone path must never report a run set")
	}
}

func TestGateRunPRsMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	prs, err := GateRunPRs(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected error when the no-mistakes binary is not on PATH")
	}
	if prs != nil {
		t.Fatal("an unrunnable binary must never report a run set")
	}
}

// Covers the uninitialized gate and atqamz/hand#60's stale renamed working_path, which print
// this text identically. An empty run set would claim the PR never went through a gate run, when
// in truth no-mistakes was never asked - the state it answers from still holds those runs.
func TestGateRunPRsNotInitializedIsAnError(t *testing.T) {
	fakeNoMistakesExit(t, "repo not initialized (run 'no-mistakes init' first)", 1)

	prs, err := GateRunPRs(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected an uninitialized gate to be an error, not an empty run set")
	}
	if prs != nil {
		t.Fatal("an uninitialized gate must never report a run set")
	}
	// The real binary exits 1 here, so the text has to be read before the exit code: reading the
	// exit code first would report a healthy binary refusing a known repo as an unrunnable binary,
	// naming a remedy that would not help.
	if !strings.Contains(err.Error(), "no-mistakes init") {
		t.Fatalf("err = %v, want it to name the init remedy for an uninitialized gate", err)
	}
	if strings.Contains(err.Error(), "binary not found or not runnable") {
		t.Fatalf("err = %v, must not blame the binary for a gate that was never initialized", err)
	}
}

func TestGateRunPRsNotAGitRepoIsAnError(t *testing.T) {
	dir := t.TempDir()
	fakeNoMistakesExit(t, "not in a git repository", 1)

	prs, err := GateRunPRs(context.Background(), dir)
	if err == nil {
		t.Fatal("expected a non-git clone path to be an error, not an empty run set")
	}
	if prs != nil {
		t.Fatal("a non-git clone path must never report a run set")
	}
	if !strings.Contains(err.Error(), "not a git repository") || !strings.Contains(err.Error(), dir) {
		t.Fatalf("err = %v, want it to name the non-git clone path %s", err, dir)
	}
	if strings.Contains(err.Error(), "binary not found or not runnable") {
		t.Fatalf("err = %v, must not blame the binary for a clone path that is not a git repository", err)
	}
}

// A no-mistakes that never answers and is killed by the caller's deadline is the case a bare
// exit-code check gets wrong: the subprocess dies, no-mistakes prints nothing, and nothing has
// been observed - it must read as an error, never as an empty (absent) run set.
func TestGateRunPRsKilledByDeadline(t *testing.T) {
	faketool.NoMistakes{Hang: []string{"runs"}}.Install(t, faketool.Bin(t))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	prs, err := GateRunPRs(ctx, t.TempDir())
	if err == nil {
		t.Fatal("expected an error when no-mistakes is killed by the caller's deadline")
	}
	if prs != nil {
		t.Fatal("a killed subprocess must never report a run set")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if strings.Contains(err.Error(), "binary not found or not runnable") {
		t.Fatalf("err = %v, must not blame the binary for a deadline hand itself set", err)
	}
}

func TestGateRunPRsContextAlreadyCancelled(t *testing.T) {
	fakeNoMistakes(t, "  completed    97-gate-visibility   758d72bf  2026-08-03 04:29  https://github.com/atqamz/hand/pull/120\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	prs, err := GateRunPRs(ctx, t.TempDir())
	if err == nil {
		t.Fatal("expected an error for an already-cancelled context")
	}
	if prs != nil {
		t.Fatal("an already-cancelled context must never report a run set")
	}
}

func TestClassifyGateRunFound(t *testing.T) {
	prURL := "https://github.com/atqamz/hand/pull/120"
	obs := ClassifyGateRun(map[string]bool{prURL: true}, nil, prURL)
	if !obs.Found() || obs.Absent() || obs.Unknown() {
		t.Fatalf("obs = %+v, want Found", obs)
	}
}

func TestClassifyGateRunAbsent(t *testing.T) {
	prURL := "https://github.com/atqamz/hand/pull/120"
	obs := ClassifyGateRun(map[string]bool{"https://github.com/atqamz/hand/pull/999": true}, nil, prURL)
	if !obs.Absent() || obs.Found() || obs.Unknown() {
		t.Fatalf("obs = %+v, want Absent", obs)
	}
}

// The state a failed lookup must never produce: a nil run set can only ever be unknown, whatever
// the failure behind it, because no list was ever read to check the PR against.
func TestClassifyGateRunUnknownOnLookupFailure(t *testing.T) {
	prURL := "https://github.com/atqamz/hand/pull/120"
	obs := ClassifyGateRun(nil, errors.New("no-mistakes gate not initialized"), prURL)
	if !obs.Unknown() || obs.Found() || obs.Absent() {
		t.Fatalf("obs = %+v, want Unknown", obs)
	}
	if !strings.Contains(obs.Reason(), "no-mistakes gate not initialized") {
		t.Fatalf("Reason() = %q, want it to name the lookup failure", obs.Reason())
	}
	if !strings.Contains(obs.Reason(), "no-mistakes runs --limit") {
		t.Fatalf("Reason() = %q, want it to name the command that was run", obs.Reason())
	}
}

// Covers atqamz/hand#240's squash-merge scenario for real: a gate run happens against a pre-squash
// branch head that is never an ancestor of main once its PR merges, and that unreachability must
// never turn a real historical FOUND into an ABSENT or an UNKNOWN.
func TestGateRunObservationSurvivesSquashMergeUnreachability(t *testing.T) {
	clonePath := t.TempDir()
	isolateGateTestGitConfig(t)

	runGateTestGit(t, clonePath, "init", "-q", "-b", "main")
	runGateTestGit(t, clonePath, "commit", "-q", "--allow-empty", "-m", "initial commit")

	runGateTestGit(t, clonePath, "checkout", "-q", "-b", "task-branch")
	if err := os.WriteFile(filepath.Join(clonePath, "feature.txt"), []byte("feature work"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGateTestGit(t, clonePath, "add", "feature.txt")
	runGateTestGit(t, clonePath, "commit", "-q", "-m", "feature work")
	preSquashHead := strings.TrimSpace(runGateTestGit(t, clonePath, "rev-parse", "HEAD"))

	runGateTestGit(t, clonePath, "checkout", "-q", "main")
	runGateTestGit(t, clonePath, "merge", "--squash", "-q", "task-branch")
	runGateTestGit(t, clonePath, "commit", "-q", "-m", "squash-merge task-branch")
	runGateTestGit(t, clonePath, "branch", "-D", "task-branch")

	// Proves the unreachability this test exists to survive, rather than assuming it: the
	// pre-squash branch head is genuinely gone from main's ancestry after the squash merge.
	isAncestor := exec.Command("git", "merge-base", "--is-ancestor", preSquashHead, "main")
	isAncestor.Dir = clonePath
	if err := isAncestor.Run(); err == nil {
		t.Fatal("expected the pre-squash branch head to be unreachable from main after a squash merge")
	}

	prURL := "https://github.com/atqamz/hand/pull/237"
	fakeNoMistakes(t, "  completed    task-branch   "+preSquashHead[:8]+"  2026-08-03 04:29  "+prURL+"\n")

	prs, err := GateRunPRs(context.Background(), clonePath)
	if err != nil {
		t.Fatal(err)
	}
	obs := ClassifyGateRun(prs, err, prURL)
	if !obs.Found() {
		t.Fatalf("obs = %+v, want Found once a completed run recorded this PR, even with its commit unreachable from main", obs)
	}
}

func isolateGateTestGitConfig(t *testing.T) {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	content := "[user]\n\tname = hand-test\n\temail = hand-test@example.invalid\n" +
		"[commit]\n\tgpgsign = false\n[init]\n\tdefaultBranch = main\n"
	if err := os.WriteFile(cfg, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func runGateTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, out)
	}
	return string(out)
}
