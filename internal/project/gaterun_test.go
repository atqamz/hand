package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/faketool"
)

const gateTestPR = "https://github.com/atqamz/hand/pull/120"
const gateTestID = "01M38X1MW6N04H8E2XQ31CCV8R"

func gateTestVerdict(id, lifecycle, pr, head, verdict, basis string) string {
	return "run_id: \"" + id + "\"\n" +
		"lifecycle: " + lifecycle + "\n" +
		"pr: \"" + pr + "\"\n" +
		"head_sha: " + head + "\n" +
		"verdict: " + verdict + "\n" +
		"basis: " + basis + "\n" +
		"reason: \"\"\n"
}

func gateTestRow(status, id, pr string) string {
	row := "  " + status + "  feature/readiness  aaaaaaaa  2026-09-24 12:00  id:" + id
	if pr != "" {
		row += "  " + pr
	}
	return row + "\n"
}

func TestGateRunPRsDiscoversDurableIDs(t *testing.T) {
	other := "01M38X1MW6N04H8E2XQ31CCV8S"
	fakeNoMistakes(t, gateTestRow("running", gateTestID, gateTestPR)+gateTestRow("failed", other, ""))
	runs, err := GateRunPRs(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := runs[gateTestPR]; len(got) != 1 || got[0] != gateTestID {
		t.Fatalf("run IDs = %v, want exact PR's durable ID", got)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %v, want only rows with a PR URL", runs)
	}
}

func TestGateRunPRsKeepsMultipleCandidatesVisible(t *testing.T) {
	other := "01M38X1MW6N04H8E2XQ31CCV8S"
	fakeNoMistakes(t, gateTestRow("running", gateTestID, gateTestPR)+gateTestRow("completed", other, gateTestPR))
	runs, err := GateRunPRs(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := runs[gateTestPR]; len(got) != 2 || got[0] != gateTestID || got[1] != other {
		t.Fatalf("run IDs = %v, want both candidates preserved", got)
	}
}

func TestGateRunPRsRejectsMalformedDiscovery(t *testing.T) {
	row := gateTestRow("running", gateTestID, gateTestPR)
	for _, tc := range []struct {
		name   string
		output string
	}{
		{name: "empty", output: ""},
		{name: "empty list with extra row", output: "  no runs yet. Push through the gate to start a pipeline:\n  git push no-mistakes <branch>\n" + row},
		{name: "missing final newline", output: strings.TrimSuffix(row, "\n")},
		{name: "extra blank row", output: row + "\n"},
		{name: "legacy missing ID", output: strings.Replace(row, "id:"+gateTestID+"  ", "", 1)},
		{name: "short ID", output: strings.Replace(row, gateTestID, "123", 1)},
		{name: "duplicate ID", output: row + row},
		{name: "unknown status", output: strings.Replace(row, "running", "mystery", 1)},
		{name: "bad short head", output: strings.Replace(row, "aaaaaaaa", "notahash", 1)},
		{name: "bad timestamp", output: strings.Replace(row, "2026-09-24", "2026-99-99", 1)},
		{name: "truncated list", output: row + "  (2 more runs, use --limit to see more)\n"},
		{name: "invalid URL", output: strings.Replace(row, gateTestPR, "not-a-url", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeNoMistakes(t, tc.output)
			runs, err := GateRunPRs(context.Background(), t.TempDir())
			if err == nil || runs != nil {
				t.Fatalf("runs = %v, err = %v, want malformed discovery to fail closed", runs, err)
			}
		})
	}
}

func TestGateRunPRsAcceptsExactEmptyList(t *testing.T) {
	fakeNoMistakes(t, "  no runs yet. Push through the gate to start a pipeline:\n  git push no-mistakes <branch>\n")
	runs, err := GateRunPRs(context.Background(), t.TempDir())
	if err != nil || len(runs) != 0 {
		t.Fatalf("runs = %v, err = %v, want complete empty list", runs, err)
	}
}

func TestClassifyGateRunStaysUnknownUntilLiveProviderVerdict(t *testing.T) {
	for _, tc := range []struct {
		name string
		runs GateRunIDs
		err  error
	}{
		{name: "candidate", runs: GateRunIDs{gateTestPR: {gateTestID}}},
		{name: "ambiguous", runs: GateRunIDs{gateTestPR: {gateTestID, "01M38X1MW6N04H8E2XQ31CCV8S"}}},
		{name: "missing", runs: GateRunIDs{}},
		{name: "lookup failed", err: errors.New("no-mistakes gate not initialized")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obs := ClassifyGateRun(tc.runs, tc.err, gateTestPR)
			if !obs.Unknown() || obs.Found() || obs.Absent() || !strings.Contains(obs.Reason(), "no-mistakes runs --limit") {
				t.Fatalf("observation = %+v, want unknown with read-only discovery probe", obs)
			}
		})
	}
}

func TestObserveGateRunFindsCandidateWithoutClaimingReadiness(t *testing.T) {
	fakeNoMistakes(t, gateTestRow("running", gateTestID, gateTestPR))
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "projects", "gated"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := ObserveGateRun(context.Background(), home, Project{Name: "gated", Mode: ModeNoMistakes}, true, gateTestPR, func(path string) (GateRunIDs, error) {
		return GateRunPRs(context.Background(), path)
	})
	if got != "unknown" {
		t.Fatalf("gate = %q, want unknown pending a live provider verdict", got)
	}
}

func TestObserveGateRunAcceptsExactCurrentCIVerdict(t *testing.T) {
	rows := gateTestRow("running", gateTestID, gateTestPR)
	verdict := gateTestVerdict(gateTestID, "running", gateTestPR, strings.Repeat("a", 40), "checks-passed", "checks")
	faketool.NoMistakes{Runs: rows, Stdout: verdict}.Install(t, faketool.Bin(t))
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "projects", "gated"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := ObserveGateRun(context.Background(), home, Project{Name: "gated", Mode: ModeNoMistakes}, true, gateTestPR, func(path string) (GateRunIDs, error) {
		return GateRunPRs(context.Background(), path)
	})
	if got != "found" {
		t.Fatalf("gate = %q, want found for current exact checks-passed run", got)
	}
}

func TestGateRunCIVerdictRequiresExactCurrentTuple(t *testing.T) {
	head := strings.Repeat("a", 40)
	good := gateTestVerdict(gateTestID, "running", gateTestPR, head, "checks-passed", "checks")
	for _, tc := range []struct {
		name   string
		output string
		exit   int
		stderr string
		want   string
	}{
		{name: "checks", output: good, want: "found"},
		{name: "update notice", output: good, stderr: "A new version of no-mistakes is available\n", want: "found"},
		{name: "declared no CI", output: gateTestVerdict(gateTestID, "running", gateTestPR, head, "checks-passed", "declared-no-ci"), want: "found"},
		{name: "wrong run", output: gateTestVerdict("01M38X1MW6N04H8E2XQ31CCV8S", "running", gateTestPR, head, "checks-passed", "checks")},
		{name: "wrong PR", output: gateTestVerdict(gateTestID, "running", "https://github.com/atqamz/hand/pull/121", head, "checks-passed", "checks")},
		{name: "completed", output: gateTestVerdict(gateTestID, "completed", gateTestPR, head, "checks-passed", "checks")},
		{name: "short head", output: gateTestVerdict(gateTestID, "running", gateTestPR, "aaaaaaaa", "checks-passed", "checks")},
		{name: "nonhex head", output: gateTestVerdict(gateTestID, "running", gateTestPR, strings.Repeat("z", 40), "checks-passed", "checks")},
		{name: "pending", output: gateTestVerdict(gateTestID, "running", gateTestPR, head, "unknown", "unknown")},
		{name: "unqualified basis", output: gateTestVerdict(gateTestID, "running", gateTestPR, head, "checks-passed", "unknown")},
		{name: "missing reason", output: strings.TrimSuffix(good, "reason: \"\"\n")},
		{name: "duplicate verdict", output: good + "verdict: checks-passed\n"},
		{name: "truncated newline", output: strings.TrimSuffix(good, "\n")},
		{name: "old provider", output: "Error: unknown command \"ci-verdict\"\n", exit: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			faketool.NoMistakes{Stdout: tc.output, Stderr: tc.stderr, Exit: tc.exit}.Install(t, faketool.Bin(t))
			got := gateRunCIVerdict(context.Background(), t.TempDir(), gateTestID, gateTestPR)
			if tc.want == "" {
				tc.want = "unknown"
			}
			if string(got) != tc.want {
				t.Fatalf("verdict = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestObserveGateRunDoesNotQueryAmbiguousCandidates(t *testing.T) {
	other := "01M38X1MW6N04H8E2XQ31CCV8S"
	logPath := filepath.Join(t.TempDir(), "invocations")
	faketool.NoMistakes{
		Runs:   gateTestRow("running", gateTestID, gateTestPR) + gateTestRow("completed", other, gateTestPR),
		Stdout: gateTestVerdict(gateTestID, "running", gateTestPR, strings.Repeat("a", 40), "checks-passed", "checks"),
		Log:    logPath,
	}.Install(t, faketool.Bin(t))
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "projects", "gated"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := ObserveGateRun(context.Background(), home, Project{Name: "gated", Mode: ModeNoMistakes}, true, gateTestPR, func(path string) (GateRunIDs, error) {
		return GateRunPRs(context.Background(), path)
	})
	if got != "unknown" {
		t.Fatalf("gate = %q, want unknown for two exact PR candidates", got)
	}
	log, err := os.ReadFile(logPath)
	if err != nil || strings.Contains(string(log), "ci-verdict") {
		t.Fatalf("ambiguous candidate queried a verdict: %q, %v", log, err)
	}
}

func TestGateRunCIVerdictTimesOutAsUnknown(t *testing.T) {
	faketool.NoMistakes{Hang: []string{"axi"}}.Install(t, faketool.Bin(t))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if got := gateRunCIVerdict(ctx, t.TempDir(), gateTestID, gateTestPR); got != "unknown" {
		t.Fatalf("verdict after timeout = %q, want unknown", got)
	}
}

func TestGateRunPRsMissingClonePath(t *testing.T) {
	runs, err := GateRunPRs(context.Background(), filepath.Join(t.TempDir(), "missing"))
	if err == nil || runs != nil {
		t.Fatalf("runs = %v, err = %v, want missing clone error", runs, err)
	}
}

func TestGateRunPRsMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	runs, err := GateRunPRs(context.Background(), t.TempDir())
	if err == nil || runs != nil {
		t.Fatalf("runs = %v, err = %v, want missing binary error", runs, err)
	}
}

func TestGateRunPRsNotInitializedIsAnError(t *testing.T) {
	fakeNoMistakesExit(t, "repo not initialized (run 'no-mistakes init' first)", 1)
	runs, err := GateRunPRs(context.Background(), t.TempDir())
	if err == nil || runs != nil || !strings.Contains(err.Error(), "no-mistakes init") {
		t.Fatalf("runs = %v, err = %v, want init remedy", runs, err)
	}
}

func TestGateRunPRsNotAGitRepoIsAnError(t *testing.T) {
	dir := t.TempDir()
	fakeNoMistakesExit(t, "not in a git repository", 1)
	runs, err := GateRunPRs(context.Background(), dir)
	if err == nil || runs != nil || !strings.Contains(err.Error(), dir) {
		t.Fatalf("runs = %v, err = %v, want invalid clone path", runs, err)
	}
}

func TestGateRunPRsKilledByDeadline(t *testing.T) {
	faketool.NoMistakes{Hang: []string{"runs"}}.Install(t, faketool.Bin(t))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	runs, err := GateRunPRs(ctx, t.TempDir())
	if runs != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runs = %v, err = %v, want deadline failure", runs, err)
	}
}

func TestGateRunPRsContextAlreadyCancelled(t *testing.T) {
	fakeNoMistakes(t, gateTestRow("running", gateTestID, gateTestPR))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runs, err := GateRunPRs(ctx, t.TempDir())
	if err == nil || runs != nil {
		t.Fatalf("runs = %v, err = %v, want cancellation failure", runs, err)
	}
}
