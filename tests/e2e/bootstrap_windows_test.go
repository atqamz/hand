//go:build e2e && windows

package e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Resolved once, relative to this package, so a test never depends on the working directory
// `go test` happened to be invoked from.
var bootstrapPS1Script = func() string {
	abs, err := filepath.Abs(filepath.Join("..", "..", "bootstrap.ps1"))
	if err != nil {
		panic(err)
	}
	return abs
}()

// Runs bootstrap.ps1 under an available native Windows PowerShell executable with an explicit,
// minimal environment - PATH and USERPROFILE (which [Environment]::GetFolderPath('UserProfile')
// reads) only - so a test can prove nothing beyond those two ever reaches the script.
func runBootstrapPS1(t *testing.T, home string, extraEnv []string, args ...string) invocation {
	t.Helper()
	psArgs := append([]string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", bootstrapPS1Script}, args...)
	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		powershell, err = exec.LookPath("pwsh.exe")
	}
	if err != nil {
		t.Fatalf("find a native PowerShell executable: %v", err)
	}
	cmd := exec.Command(powershell, psArgs...)
	env := append([]string{"PATH=" + os.Getenv("PATH"), "USERPROFILE=" + home, "SystemRoot=" + os.Getenv("SystemRoot")}, extraEnv...)
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run bootstrap.ps1 %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	t.Logf("$ bootstrap.ps1 %s\n  exit %d\n  stdout: %s\n  stderr: %s",
		strings.Join(args, " "), code, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()))
	return invocation{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// Puts a copy of the already-built hand binary on dir as hand.exe, the extension native Windows
// PATH resolution (and a real install.ps1) requires; a bare "hand" is never found by Get-Command.
func installFakeHandExe(t *testing.T, dir string) {
	t.Helper()
	data, err := os.ReadFile(handBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hand.exe"), data, 0o755); err != nil {
		t.Fatal(err)
	}
}

// Drops a no-op .cmd on dir under name, standing in for an installed tool or an installed,
// authenticated coding-agent harness: bootstrap.ps1 only ever needs Get-Command to find it.
func installFakeCmdExe(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".cmd"), []byte("@exit /b 0\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func isFleetHomeWindows(fleet string) bool {
	_, err := os.Stat(filepath.Join(fleet, "state", "hand.db"))
	return err == nil
}

func TestBootstrapPS1HappyPathReconcilesFleetAndPrintsTheInstalledHarness(t *testing.T) {
	dir := binDir(t)
	installFakeHandExe(t, dir)
	installFakeCmdExe(t, dir, "treehouse")
	installFakeCmdExe(t, dir, "herdr")
	installFakeCmdExe(t, dir, "claude")

	home := t.TempDir()
	fleet := filepath.Join(home, "secondhand-fleet")

	got := runBootstrapPS1(t, home, nil, "-Fleet", fleet, "-Yes")
	if got.code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout %q, stderr %q)", got.code, got.stdout, got.stderr)
	}
	if !isFleetHomeWindows(fleet) {
		t.Fatalf("%s was not initialized as a fleet home", fleet)
	}
	for _, want := range []string{"Secondhand is ready.", "cd " + fleet, "claude", "ready: true"} {
		if !strings.Contains(got.stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", got.stdout, want)
		}
	}
}

func TestBootstrapPS1RefusesAForeignNonEmptyFleetTarget(t *testing.T) {
	dir := binDir(t)
	installFakeHandExe(t, dir)
	installFakeCmdExe(t, dir, "treehouse")
	installFakeCmdExe(t, dir, "herdr")

	home := t.TempDir()
	fleet := t.TempDir()
	if err := os.WriteFile(filepath.Join(fleet, "unrelated.txt"), []byte("not a fleet\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := runBootstrapPS1(t, home, nil, "-Fleet", fleet, "-Yes")
	if got.code != 1 {
		t.Fatalf("exit = %d, want 1 (stdout %q, stderr %q)", got.code, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "refusing to adopt it") {
		t.Fatalf("stdout = %q, want a refusal for the foreign non-empty target", got.stdout)
	}
	if isFleetHomeWindows(fleet) {
		t.Fatal("bootstrap.ps1 mutated a foreign non-empty target instead of refusing it")
	}
}

func TestBootstrapPS1CheckModeNeverMutatesAnAbsentFleetTarget(t *testing.T) {
	dir := binDir(t)
	installFakeHandExe(t, dir)
	installFakeCmdExe(t, dir, "treehouse")
	installFakeCmdExe(t, dir, "herdr")
	installFakeCmdExe(t, dir, "claude")

	home := t.TempDir()
	fleet := filepath.Join(home, "secondhand-fleet")

	got := runBootstrapPS1(t, home, nil, "-Fleet", fleet, "-Check")
	if got.code != 0 {
		t.Fatalf("exit = %d, want 0 for check mode (stdout %q, stderr %q)", got.code, got.stdout, got.stderr)
	}
	if _, err := os.Stat(fleet); !os.IsNotExist(err) {
		t.Fatalf("-Check created %s; check mode must never mutate", fleet)
	}
}

func TestBootstrapPS1DeclinesMissingDependenciesNonInteractivelyWithoutYesAndFailsClosed(t *testing.T) {
	dir := binDir(t)
	installFakeHandExe(t, dir)
	installFakeCmdExe(t, dir, "herdr")
	// treehouse deliberately left missing, and PowerShell's stdin is not an interactive console
	// under exec.Command, so bootstrap must decline installing it rather than hang or guess consent.

	home := t.TempDir()
	fleet := filepath.Join(home, "secondhand-fleet")

	got := runBootstrapPS1(t, home, nil, "-Fleet", fleet)
	if got.code != 1 {
		t.Fatalf("exit = %d, want 1 (stdout %q, stderr %q)", got.code, got.stdout, got.stderr)
	}
	for _, want := range []string{
		"declining to install any of the above",
		"- treehouse",
		"recover the items above, then rerun",
	} {
		if !strings.Contains(got.stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", got.stdout, want)
		}
	}
	if !isFleetHomeWindows(fleet) {
		t.Fatal("hand init did not run even though only a foundational dependency was missing; a partial setup must still be retained and reconciled")
	}
}

func TestBootstrapPS1NeverInstallsAHarnessOrNoMistakesAutomatically(t *testing.T) {
	dir := binDir(t)
	installFakeHandExe(t, dir)
	installFakeCmdExe(t, dir, "treehouse")
	installFakeCmdExe(t, dir, "herdr")
	// No coding-agent harness and no no-mistakes on PATH at all.

	home := t.TempDir()
	fleet := filepath.Join(home, "secondhand-fleet")

	got := runBootstrapPS1(t, home, nil, "-Fleet", fleet, "-Yes")
	if got.code != 1 {
		t.Fatalf("exit = %d, want 1: no harness is installed, so the fleet cannot be ready (stdout %q, stderr %q)", got.code, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "- harness") {
		t.Fatalf("stdout = %q, want the missing harness reported as blocking", got.stdout)
	}
	for _, name := range []string{"claude", "codex", "grok", "pi", "opencode", "no-mistakes"} {
		if strings.Contains(got.stdout, "installing "+name) {
			t.Fatalf("stdout = %q, want bootstrap to never attempt installing a harness or no-mistakes", got.stdout)
		}
		if _, err := os.Stat(filepath.Join(dir, name+".cmd")); err == nil {
			t.Fatalf("%s was created on PATH; bootstrap must only ever detect harnesses, never install one", name)
		}
	}
}

func TestBootstrapPS1AcceptsAFleetTargetPathContainingSpaces(t *testing.T) {
	dir := binDir(t)
	installFakeHandExe(t, dir)
	installFakeCmdExe(t, dir, "treehouse")
	installFakeCmdExe(t, dir, "herdr")
	installFakeCmdExe(t, dir, "codex")

	home := t.TempDir()
	fleet := filepath.Join(home, "my secondhand fleet")

	got := runBootstrapPS1(t, home, nil, "-Fleet", fleet, "-Yes")
	if got.code != 0 {
		t.Fatalf("exit = %d, want 0 for a fleet path containing spaces (stdout %q, stderr %q)", got.code, got.stdout, got.stderr)
	}
	if !isFleetHomeWindows(fleet) {
		t.Fatalf("%s was not initialized", fleet)
	}
}

func TestBootstrapPS1NeverForwardsAmbientSecretsIntoItsOutput(t *testing.T) {
	dir := binDir(t)
	installFakeHandExe(t, dir)
	installFakeCmdExe(t, dir, "treehouse")
	installFakeCmdExe(t, dir, "herdr")
	installFakeCmdExe(t, dir, "claude")

	home := t.TempDir()
	fleet := filepath.Join(home, "secondhand-fleet")
	const secret = "poison-token-do-not-print-1x2y3z"

	got := runBootstrapPS1(t, home, []string{"GH_TOKEN=" + secret, "ANTHROPIC_API_KEY=" + secret}, "-Fleet", fleet, "-Yes")
	if got.code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout %q, stderr %q)", got.code, got.stdout, got.stderr)
	}
	if strings.Contains(got.stdout, secret) || strings.Contains(got.stderr, secret) {
		t.Fatal("bootstrap.ps1 leaked an ambient credential into its own output")
	}
}

func TestBootstrapPS1ReconcilesAnExistingFleetIdempotently(t *testing.T) {
	dir := binDir(t)
	installFakeHandExe(t, dir)
	installFakeCmdExe(t, dir, "treehouse")
	installFakeCmdExe(t, dir, "herdr")
	installFakeCmdExe(t, dir, "claude")

	home := t.TempDir()
	fleet := filepath.Join(home, "secondhand-fleet")

	first := runBootstrapPS1(t, home, nil, "-Fleet", fleet, "-Yes")
	if first.code != 0 {
		t.Fatalf("first run: exit = %d, want 0 (stdout %q, stderr %q)", first.code, first.stdout, first.stderr)
	}

	second := runBootstrapPS1(t, home, nil, "-Fleet", fleet, "-Yes")
	if second.code != 0 {
		t.Fatalf("second run: exit = %d, want 0 (stdout %q, stderr %q)", second.code, second.stdout, second.stderr)
	}
	if !strings.Contains(second.stdout, "agents_md: unchanged") {
		t.Fatalf("second run stdout = %q, want hand init to report the already-canonical AGENTS.md unchanged", second.stdout)
	}
	if !strings.Contains(second.stdout, "Secondhand is ready.") {
		t.Fatalf("second run stdout = %q, want a ready fleet on a repeat run", second.stdout)
	}
}
