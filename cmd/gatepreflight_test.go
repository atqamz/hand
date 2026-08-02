package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/project"
	"github.com/atqamz/secondhand/internal/state"
)

// fakeNoMistakesPath writes a fake no-mistakes binary that only answers `status`, mirroring the
// real binary's observed behavior documented in internal/project.GateStatus: it always exits 0,
// so the outcome is read from stdout text rather than the exit code. Returns a PATH with the fake
// binary's directory prepended ahead of the real PATH: the script's own "cat" still needs to
// resolve, and the fake must win the lookup over any real no-mistakes already on this machine.
func fakeNoMistakesPath(t *testing.T, stdout string) string {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\ncat <<'EOF'\n" + stdout + "\nEOF\n"
	if err := os.WriteFile(filepath.Join(bin, "no-mistakes"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin + string(os.PathListSeparator) + os.Getenv("PATH")
}

// fakeHerdrPaneDone fakes only "pane get", answering the pane as done (not busy), enough for
// promote's precondition check to pass through to gatePreflight without needing the rest of a
// clean promote's herdr calls, which gatePreflight's refusal preempts.
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

// setupSpawnHomeGate registers a no-mistakes-mode project and nothing else: gatePreflight fires
// in spawn before state.Claim, the brief check, or any herdr/treehouse call, so none of those need
// to exist for these tests. noMistakesPath becomes PATH verbatim, letting each test control
// exactly whether a fake no-mistakes binary is reachable.
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

// setupPromoteHomeGate mirrors setupPromoteHome but registers a no-mistakes-mode project and
// skips the worktree/treehouse setup: gatePreflight fires in promote after the report, pane-busy,
// and brief checks but before worktree.Get, so those three preconditions must be satisfied while
// nothing past gatePreflight needs to exist. noMistakesPath becomes PATH verbatim except for the
// fake herdr binary this helper always adds, letting each test control whether no-mistakes is
// reachable.
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

	herdrBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(herdrBin, "herdr"), []byte(fakeHerdrPaneDone), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", herdrBin+string(os.PathListSeparator)+noMistakesPath)
	t.Chdir(home)
	mkFleetDirs(t, home)
	return home
}

// TestSpawnRefusesWhenNoMistakesGateNotInitialized stands in for both real histories from
// atqamz/secondhand#60 (never-initialized project, and a project whose working_path went stale
// after the fleet home was renamed): both were checked against the real binary and emit the same
// status text, so one refusal test here covers both.
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

// TestSpawnSkipGateCheckBypassesRefusalAndWarns pairs the two halves of the escape hatch: the
// not-initialized gate no longer refuses, and the bypass still announces itself on stderr, which
// is the only thing that keeps it visible in a transcript.
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
