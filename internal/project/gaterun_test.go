package project

import (
	"path/filepath"
	"testing"
)

func TestGateRunPRsCollectsCompletedRunPRs(t *testing.T) {
	fakeNoMistakes(t, "  completed    97-gate-visibility   758d72bf  2026-08-03 04:29  https://github.com/atqamz/secondhand/pull/120\n"+
		"  running      74-workspace-leak    b2f584f9  2026-08-03 04:20\n")

	prs, err := GateRunPRs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !prs["https://github.com/atqamz/secondhand/pull/120"] {
		t.Fatal("expected a completed run's own recorded PR URL to be collected")
	}
	if prs["https://github.com/atqamz/secondhand/pull/999"] {
		t.Fatal("expected no entry for a PR no completed run recorded")
	}
}

// TestGateRunPRsIgnoresNonCompletedRuns covers a run that recorded a PR URL but never reached
// completed - running or failed both leave the gate un-cleared for that commit, so neither should
// count as evidence a run happened.
func TestGateRunPRsIgnoresNonCompletedRuns(t *testing.T) {
	fakeNoMistakes(t, "  failed       97-gate-visibility   758d72bf  2026-08-03 04:29  https://github.com/atqamz/secondhand/pull/120\n")

	prs, err := GateRunPRs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if prs["https://github.com/atqamz/secondhand/pull/120"] {
		t.Fatal("a failed run must not count as gate coverage even though it recorded the PR URL")
	}
}

func TestGateRunPRsMissingClonePath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	prs, err := GateRunPRs(missing)
	if err == nil {
		t.Fatal("expected error for a clone path that does not exist")
	}
	if prs != nil {
		t.Fatal("a missing clone path must never report a run set")
	}
}

func TestGateRunPRsMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	prs, err := GateRunPRs(t.TempDir())
	if err == nil {
		t.Fatal("expected error when the no-mistakes binary is not on PATH")
	}
	if prs != nil {
		t.Fatal("an unrunnable binary must never report a run set")
	}
}

// TestGateRunPRsNotInitializedIsAnError covers the uninitialized gate and the stale renamed
// working_path from atqamz/secondhand#60, which print this text identically. An empty run set here
// would render as the far stronger claim that the PR never went through a gate run, when in truth
// no-mistakes was never asked - the state it would have answered from still holds those runs.
func TestGateRunPRsNotInitializedIsAnError(t *testing.T) {
	fakeNoMistakes(t, "repo not initialized (run 'no-mistakes init' first)")

	prs, err := GateRunPRs(t.TempDir())
	if err == nil {
		t.Fatal("expected an uninitialized gate to be an error, not an empty run set")
	}
	if prs != nil {
		t.Fatal("an uninitialized gate must never report a run set")
	}
}

func TestGateRunPRsNotAGitRepoIsAnError(t *testing.T) {
	fakeNoMistakes(t, "not in a git repository")

	prs, err := GateRunPRs(t.TempDir())
	if err == nil {
		t.Fatal("expected a non-git clone path to be an error, not an empty run set")
	}
	if prs != nil {
		t.Fatal("a non-git clone path must never report a run set")
	}
}
