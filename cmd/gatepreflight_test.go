package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
)

// Writes a fake no-mistakes binary that answers every subcommand with the same text, mirroring the
// real behavior internal/project.GateStatus documents: `no-mistakes status` exits 0 whether or not the
// repo is initialized, so the outcome is read from stdout text rather than the exit code.
func fakeNoMistakesPath(t *testing.T, stdout string) string {
	// Prepended ahead of the real PATH, not replacing it: the script's own "cat" still needs to resolve,
	// and the fake must win the lookup over any real no-mistakes already on this machine.
	return fakeNoMistakesPathExit(t, stdout, 0)
}

// fakeNoMistakesPath with an explicit exit code, for the invocations the real binary refuses non-zero:
// `no-mistakes runs` exits 1 on both "repo not initialized" and "not in a git repository", where
// `no-mistakes status` exits 0 printing the same text.
func fakeNoMistakesPathExit(t *testing.T, stdout string, code int) string {
	t.Helper()
	bin := t.TempDir()
	// GateRunPRs reads the refusal from the text either way, so the fake reproduces the exit code rather
	// than flattening every refusal to 0.
	script := fmt.Sprintf("#!/bin/sh\ncat <<'EOF'\n%s\nEOF\nexit %d\n", stdout, code)
	if err := os.WriteFile(filepath.Join(bin, "no-mistakes"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin + string(os.PathListSeparator) + os.Getenv("PATH")
}

// Fakes only "pane get", answering the pane as done (not busy), enough for promote's precondition
// check to pass through to gatePreflight without needing the rest of a clean promote's herdr calls,
// which gatePreflight's refusal preempts.
const fakeHerdrPaneDone = `#!/bin/sh
cmd="$1 $2"
case "$cmd" in
"pane get")
	printf '{"id":"cli:1","result":{"pane":{"pane_id":"%s","tab_id":"wA:tOld","workspace_id":"wA","agent_status":"done"}}}' "$3"
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`

// Registers a no-mistakes-mode project and nothing else: gatePreflight fires in spawn before
// state.Claim, the brief check, or any herdr/treehouse call, so none of those need to exist here.
// noMistakesPath becomes PATH verbatim, letting each test control whether the fake is reachable.
func setupSpawnHomeGate(t *testing.T, noMistakesPath string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "gated", URL: "https://example.com/gated.git", Mode: project.ModeNoMistakes}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "gated"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", noMistakesPath)
	t.Chdir(home)
	mkFleetDirs(t, home)
	return home
}

// Mirrors setupPromoteHome but registers a no-mistakes-mode project and skips the worktree/treehouse
// setup: gatePreflight fires in promote after the report, pane-busy, and brief checks but before
// worktree.Get, so those three must be satisfied while nothing past gatePreflight needs to exist.
func setupPromoteHomeGate(t *testing.T, noMistakesPath string) string {
	t.Helper()
	useFastLaunchPolling(t)
	home := t.TempDir()

	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "report.md"), []byte("scout findings"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "brief.md"), []byte("implement the fix"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "gated", URL: "https://example.com/gated.git", Mode: project.ModeNoMistakes}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "gated"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{
		ID:      "task-1",
		Project: "gated",
		Kind:    state.KindScout,
		Herdr:   state.Herdr{WorkspaceID: "wA", TabID: "wA:tOld", PaneID: "wA:pOld"},
	}); err != nil {
		t.Fatal(err)
	}

	// noMistakesPath becomes PATH verbatim except for the fake herdr binary this helper always adds,
	// letting each test control whether no-mistakes is reachable.
	herdrBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(herdrBin, "herdr"), []byte(fakeHerdrPaneDone), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", herdrBin+string(os.PathListSeparator)+noMistakesPath)
	t.Chdir(home)
	mkFleetDirs(t, home)
	return home
}

// Stands in for both real histories from atqamz/hand#60 (a never-initialized project, and one
// whose working_path went stale after the fleet home was renamed): both were checked against the real
// binary and emit the same status text, so one refusal test covers both.
func TestSpawnRefusesWhenNoMistakesGateNotInitialized(t *testing.T) {
	path := fakeNoMistakesPath(t, "repo not initialized (run 'no-mistakes init' first)")
	home := setupSpawnHomeGate(t, path)

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "gated"})
	err := cmd.Execute()
	assertExitCode3(t, err)

	clonePath := filepath.Join(home, "projects", "gated")
	wantRemedy := "no-mistakes init"
	if !strings.Contains(err.Error(), wantRemedy) || !strings.Contains(err.Error(), clonePath) {
		t.Fatalf("err = %v, want it to name %q and the remedy %q", err, clonePath, wantRemedy)
	}
}

func TestSpawnRefusesDistinctlyWhenNoMistakesBinaryMissing(t *testing.T) {
	setupSpawnHomeGate(t, t.TempDir())

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "gated"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error when no-mistakes is not runnable")
	}
	if exitCodeFor(t, err) == 3 && strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("err = %v, must not read as the gate-not-initialized case", err)
	}
}

func TestSpawnProceedsWhenNoMistakesGateReady(t *testing.T) {
	path := fakeNoMistakesPath(t, "gate: ready\n\n  no active run")
	setupSpawnHomeGate(t, path)

	cmd := newSpawnCmd()
	cmd.SetArgs([]string{"task-1", "gated"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected spawn to still fail past the gate check (no brief, no herdr fake)")
	}
	if exitCodeFor(t, err) == 3 && strings.Contains(err.Error(), "gate") {
		t.Fatalf("err = %v, gate check should have passed and failed later for an unrelated reason", err)
	}
}

// Pairs the two halves of the escape hatch: the not-initialized gate no longer refuses, and the
// bypass still announces itself on stderr, which is the only thing that keeps it visible in a
// transcript.
func TestSpawnSkipGateCheckBypassesRefusalAndWarns(t *testing.T) {
	path := fakeNoMistakesPath(t, "repo not initialized (run 'no-mistakes init' first)")
	setupSpawnHomeGate(t, path)

	var errOut bytes.Buffer
	cmd := newSpawnCmd()
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"task-1", "gated", "--skip-gate-check"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected spawn to still fail past the gate check (no brief, no herdr fake)")
	}
	if exitCodeFor(t, err) == 3 && strings.Contains(err.Error(), "gate") {
		t.Fatalf("err = %v, --skip-gate-check should have bypassed the gate refusal", err)
	}
	if !strings.Contains(errOut.String(), "--skip-gate-check") || !strings.Contains(errOut.String(), "gated") {
		t.Fatalf("stderr = %q, want a warning naming the flag and the project", errOut.String())
	}
}

func TestPromoteRefusesWhenNoMistakesGateNotInitialized(t *testing.T) {
	t.Setenv("HAND_HARNESS", "unknown")
	path := fakeNoMistakesPath(t, "repo not initialized (run 'no-mistakes init' first)")
	home := setupPromoteHomeGate(t, path)

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	assertExitCode3(t, err)

	clonePath := filepath.Join(home, "projects", "gated")
	wantRemedy := "no-mistakes init"
	if !strings.Contains(err.Error(), wantRemedy) || !strings.Contains(err.Error(), clonePath) {
		t.Fatalf("err = %v, want it to name %q and the remedy %q", err, clonePath, wantRemedy)
	}
}

// Pins the ordering from atqamz/hand#156: a gate refusal must preempt launch warnings because
// no launch will occur.
func TestPromoteGateRefusalWarnsNothingAboutTheLaunch(t *testing.T) {
	path := fakeNoMistakesPath(t, "repo not initialized (run 'no-mistakes init' first)")
	setupPromoteHomeGate(t, path)

	var errOut bytes.Buffer
	cmd := newPromoteCmd()
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"task-1", "--harness", "codex"})
	err := cmd.Execute()
	assertExitCode3(t, err)

	if strings.Contains(errOut.String(), "cannot carry") {
		t.Fatalf("stderr = %q, want no launch warning on a promote that never launches", errOut.String())
	}
}

func TestPromoteRefusesDistinctlyWhenNoMistakesBinaryMissing(t *testing.T) {
	setupPromoteHomeGate(t, t.TempDir())

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error when no-mistakes is not runnable")
	}
	if exitCodeFor(t, err) == 3 && strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("err = %v, must not read as the gate-not-initialized case", err)
	}
}
