package project

import (
	"path/filepath"
	"testing"
)

func TestGateRanForPRMatchesCompletedRun(t *testing.T) {
	fakeNoMistakes(t, "  completed    97-gate-visibility   758d72bf  2026-08-03 04:29  https://github.com/atqamz/secondhand/pull/120\n"+
		"  running      74-workspace-leak    b2f584f9  2026-08-03 04:20\n")

	got, err := GateRanForPR(t.TempDir(), "https://github.com/atqamz/secondhand/pull/120")
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("expected a completed run with this exact PR URL to be found")
	}
}

func TestGateRanForPRNoMatchingRun(t *testing.T) {
	fakeNoMistakes(t, "  completed    97-gate-visibility   758d72bf  2026-08-03 04:29  https://github.com/atqamz/secondhand/pull/120\n")

	got, err := GateRanForPR(t.TempDir(), "https://github.com/atqamz/secondhand/pull/999")
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("expected no match for a PR no completed run recorded")
	}
}

// TestGateRanForPRIgnoresNonCompletedRuns covers a run that shares a PR URL but never reached
// completed - running or failed both leave the gate un-cleared for that commit, so neither should
// count as evidence a run happened.
func TestGateRanForPRIgnoresNonCompletedRuns(t *testing.T) {
	fakeNoMistakes(t, "  failed       97-gate-visibility   758d72bf  2026-08-03 04:29  https://github.com/atqamz/secondhand/pull/120\n")

	got, err := GateRanForPR(t.TempDir(), "https://github.com/atqamz/secondhand/pull/120")
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("a failed run must not count as gate coverage even if it shares the PR URL")
	}
}

func TestGateRanForPREmptyPRNeverRuns(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	got, err := GateRanForPR(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("an empty PR URL can never match")
	}
}

func TestGateRanForPRMissingClonePath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	got, err := GateRanForPR(missing, "https://github.com/atqamz/secondhand/pull/120")
	if err == nil {
		t.Fatal("expected error for a clone path that does not exist")
	}
	if got {
		t.Fatal("a missing clone path must never report a match")
	}
}
