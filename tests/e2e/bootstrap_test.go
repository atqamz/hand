//go:build e2e

package e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
)

// Resolved once, relative to this package, so a test never depends on the working directory
// `go test` happened to be invoked from.
var bootstrapScript = func() string {
	abs, err := filepath.Abs(filepath.Join("..", "..", "bootstrap.sh"))
	if err != nil {
		panic(err)
	}
	return abs
}()

// Runs bootstrap.sh under an explicit, minimal environment - PATH and HOME only - so a test can
// prove nothing beyond those two ever reaches the script.
func runBootstrap(t *testing.T, home string, extraEnv []string, args ...string) invocation {
	t.Helper()
	seedPrivateRuntime(t, home)
	cmd := exec.Command("sh", append([]string{bootstrapScript}, args...)...)
	env := append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TERM=dumb", "SECONDHAND_HOME=" + filepath.Join(home, ".secondhand")}, extraEnv...)
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run bootstrap.sh %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	t.Logf("$ bootstrap.sh %s\n  exit %d\n  stdout: %s\n  stderr: %s",
		strings.Join(args, " "), code, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()))
	return invocation{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// Puts a symlink to the already-built hand binary on dir, the way a real install.sh would leave
// hand on PATH.
func installFakeHand(t *testing.T, dir string) {
	t.Helper()
	if err := os.Symlink(handBin, filepath.Join(dir, "hand")); err != nil {
		t.Fatal(err)
	}
}

// Drops a no-op executable on dir under name, standing in for an installed, authenticated
// coding-agent harness: bootstrap only ever needs to see it on PATH.
func installFakeHarness(t *testing.T, dir, name string) {
	t.Helper()
	writeFakeBin(t, dir, name, "exit 0\n")
}

// Drops a fake curl that always fails, proving bootstrap does not use curl for core runtime setup.
func installFakeCurlFailing(t *testing.T, dir string) {
	t.Helper()
	writeFakeBin(t, dir, "curl", "exit 1\n")
}

func isFleetHome(fleet string) bool {
	_, err := os.Stat(filepath.Join(fleet, "state", "hand.db"))
	return err == nil
}

func TestBootstrapHappyPathReconcilesFleetAndPrintsTheInstalledHarness(t *testing.T) {
	dir := binDir(t)
	installFakeHand(t, dir)
	faketool.Treehouse{}.Install(t, dir)
	faketool.Herdr{}.Install(t, dir)
	installFakeHarness(t, dir, "claude")

	home := t.TempDir()
	fleet := filepath.Join(home, "secondhand-fleet")

	got := runBootstrap(t, home, nil, "--fleet", fleet, "--yes")
	if got.code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", got.code, got.stderr)
	}
	if !isFleetHome(fleet) {
		t.Fatalf("%s was not initialized as a fleet home", fleet)
	}
	for _, want := range []string{"Secondhand is ready.", "cd " + fleet, "claude", "ready: true"} {
		if !strings.Contains(got.stderr, want) {
			t.Fatalf("stderr = %q, want it to contain %q", got.stderr, want)
		}
	}
}

func TestBootstrapRefusesAForeignNonEmptyFleetTarget(t *testing.T) {
	dir := binDir(t)
	installFakeHand(t, dir)
	faketool.Treehouse{}.Install(t, dir)
	faketool.Herdr{}.Install(t, dir)

	home := t.TempDir()
	fleet := t.TempDir()
	if err := os.WriteFile(filepath.Join(fleet, "unrelated.txt"), []byte("not a fleet\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := runBootstrap(t, home, nil, "--fleet", fleet, "--yes")
	if got.code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "refusing to adopt it") {
		t.Fatalf("stderr = %q, want a refusal for the foreign non-empty target", got.stderr)
	}
	if isFleetHome(fleet) {
		t.Fatal("bootstrap.sh mutated a foreign non-empty target instead of refusing it")
	}
	if _, err := os.Stat(filepath.Join(fleet, "unrelated.txt")); err != nil {
		t.Fatalf("bootstrap.sh disturbed the foreign target's own content: %v", err)
	}
}

func TestBootstrapCheckModeNeverMutatesAnAbsentFleetTarget(t *testing.T) {
	dir := binDir(t)
	installFakeHand(t, dir)
	faketool.Treehouse{}.Install(t, dir)
	faketool.Herdr{}.Install(t, dir)
	installFakeHarness(t, dir, "claude")

	home := t.TempDir()
	fleet := filepath.Join(home, "secondhand-fleet")

	got := runBootstrap(t, home, nil, "--fleet", fleet, "--check")
	if got.code != 0 {
		t.Fatalf("exit = %d, want 0 for check mode (stderr %q)", got.code, got.stderr)
	}
	if _, err := os.Stat(fleet); !os.IsNotExist(err) {
		t.Fatalf("--check created %s; check mode must never mutate", fleet)
	}
}

func TestBootstrapUsesPrivateRuntimeWithoutCoreToolsOnPath(t *testing.T) {
	dir := binDir(t)
	installFakeHand(t, dir)
	installFakeHarness(t, dir, "claude")

	home := t.TempDir()
	fleet := filepath.Join(home, "secondhand-fleet")

	got := runBootstrap(t, home, nil, "--fleet", fleet)
	if got.code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", got.code, got.stderr)
	}
	for _, notWant := range []string{"declining to install", "installing treehouse", "installing herdr", "installing git"} {
		if strings.Contains(got.stderr, notWant) {
			t.Fatalf("stderr = %q, must not contain legacy PATH installation %q", got.stderr, notWant)
		}
	}
	if !strings.Contains(got.stderr, "ready: true") {
		t.Fatalf("stderr = %q, want private runtime readiness", got.stderr)
	}
}

func TestBootstrapDoesNotUseCurlForCoreRuntime(t *testing.T) {
	dir := binDir(t)
	installFakeHand(t, dir)
	installFakeHarness(t, dir, "claude")
	installFakeCurlFailing(t, dir)

	home := t.TempDir()
	fleet := filepath.Join(home, "secondhand-fleet")

	got := runBootstrap(t, home, nil, "--fleet", fleet, "--yes")
	if got.code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", got.code, got.stderr)
	}
	if !isFleetHome(fleet) {
		t.Fatalf("%s was not initialized", fleet)
	}
	for _, notWant := range []string{"installing treehouse", "installing herdr", "installing git", "installing dependencies"} {
		if strings.Contains(got.stderr, notWant) {
			t.Fatalf("stderr = %q, must not install core tools through curl", got.stderr)
		}
	}
	if !strings.Contains(got.stderr, "ready: true") {
		t.Fatalf("stderr = %q, want readiness from the private runtime", got.stderr)
	}
}

func TestBootstrapNeverInstallsAHarnessOrNoMistakesAutomatically(t *testing.T) {
	dir := binDir(t)
	installFakeHand(t, dir)
	faketool.Treehouse{}.Install(t, dir)
	faketool.Herdr{}.Install(t, dir)
	// No coding-agent harness and no no-mistakes on PATH at all.

	home := t.TempDir()
	fleet := filepath.Join(home, "secondhand-fleet")

	got := runBootstrap(t, home, nil, "--fleet", fleet, "--yes")
	if got.code != 1 {
		t.Fatalf("exit = %d, want 1: no harness is installed, so the fleet cannot be ready (stderr %q)", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "- harness") {
		t.Fatalf("stderr = %q, want the missing harness reported as blocking", got.stderr)
	}
	for _, name := range []string{"claude", "codex", "grok", "pi", "opencode", "no-mistakes"} {
		if strings.Contains(got.stderr, "installing "+name) {
			t.Fatalf("stderr = %q, want bootstrap to never attempt installing a harness or no-mistakes", got.stderr)
		}
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Fatalf("%s was created on PATH; bootstrap must only ever detect harnesses, never install one", name)
		}
	}
}

func TestBootstrapAcceptsAFleetTargetPathContainingSpaces(t *testing.T) {
	dir := binDir(t)
	installFakeHand(t, dir)
	faketool.Treehouse{}.Install(t, dir)
	faketool.Herdr{}.Install(t, dir)
	installFakeHarness(t, dir, "codex")

	home := t.TempDir()
	fleet := filepath.Join(home, "my secondhand fleet")

	got := runBootstrap(t, home, nil, "--fleet", fleet, "--yes")
	if got.code != 0 {
		t.Fatalf("exit = %d, want 0 for a fleet path containing spaces (stderr %q)", got.code, got.stderr)
	}
	if !isFleetHome(fleet) {
		t.Fatalf("%s was not initialized", fleet)
	}
}

func TestBootstrapNeverForwardsAmbientSecretsIntoItsOutput(t *testing.T) {
	dir := binDir(t)
	installFakeHand(t, dir)
	faketool.Treehouse{}.Install(t, dir)
	faketool.Herdr{}.Install(t, dir)
	installFakeHarness(t, dir, "claude")

	home := t.TempDir()
	fleet := filepath.Join(home, "secondhand-fleet")
	const secret = "poison-token-do-not-print-1x2y3z"

	got := runBootstrap(t, home, []string{"GH_TOKEN=" + secret, "ANTHROPIC_API_KEY=" + secret}, "--fleet", fleet, "--yes")
	if got.code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", got.code, got.stderr)
	}
	if strings.Contains(got.stdout, secret) || strings.Contains(got.stderr, secret) {
		t.Fatal("bootstrap.sh leaked an ambient credential into its own output")
	}
}

func TestBootstrapReconcilesAnExistingFleetIdempotently(t *testing.T) {
	dir := binDir(t)
	installFakeHand(t, dir)
	faketool.Treehouse{}.Install(t, dir)
	faketool.Herdr{}.Install(t, dir)
	installFakeHarness(t, dir, "claude")

	home := t.TempDir()
	fleet := filepath.Join(home, "secondhand-fleet")

	first := runBootstrap(t, home, nil, "--fleet", fleet, "--yes")
	if first.code != 0 {
		t.Fatalf("first run: exit = %d, want 0 (stderr %q)", first.code, first.stderr)
	}

	second := runBootstrap(t, home, nil, "--fleet", fleet, "--yes")
	if second.code != 0 {
		t.Fatalf("second run: exit = %d, want 0 (stderr %q)", second.code, second.stderr)
	}
	if !strings.Contains(second.stderr, "agents_md: unchanged") {
		t.Fatalf("second run stderr = %q, want hand init to report the already-canonical AGENTS.md unchanged", second.stderr)
	}
	if !strings.Contains(second.stderr, "Secondhand is ready.") {
		t.Fatalf("second run stderr = %q, want a ready fleet on a repeat run", second.stderr)
	}
}
