//go:build e2e

package e2e

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
	cmd := exec.Command("sh", append([]string{"-s", "--"}, args...)...)
	cmd.Dir = home
	env := append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TERM=dumb", "SECONDHAND_HOME=" + filepath.Join(home, ".secondhand")}, extraEnv...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(boundBootstrap(t, bootstrapScript))
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

func boundBootstrap(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReplacer(
		"@HAND_RELEASE_TAG@", "v1.2.3",
		"@HAND_RELEASE_VERSION@", "1.2.3",
		"@HAND_RELEASE_COMMIT@", "0123456789abcdef0123456789abcdef01234567",
		"@HAND_RELEASE_RUNTIME_ID@", "rd2343fe130ff5ba2",
	).Replace(string(data))
}

func writeHandArchive(t *testing.T, path string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	data, err := os.ReadFile(handBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.WriteHeader(&tar.Header{Name: "hand", Mode: 0o755, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
}

func sha256Path(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func installReleaseCurl(t *testing.T, dir, archive, checksums, logPath string, interrupted bool) {
	t.Helper()
	archiveCopy := fmt.Sprintf("cp %s \"$out\"", shellSingleQuote(archive))
	if interrupted {
		archiveCopy = fmt.Sprintf("dd if=%s of=\"$out\" bs=1 count=8 2>/dev/null; exit 1", shellSingleQuote(archive))
	}
	body := fmt.Sprintf(`set -eu
out=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) out=$2; shift 2 ;;
    *) url=$1; shift ;;
  esac
done
printf '%%s\n' "$url" >> %s
case "$url" in
  https://github.com/atqamz/hand/releases/download/v1.2.3/hand-linux-amd64.tar.gz)
    %s
    ;;
  https://github.com/atqamz/hand/releases/download/v1.2.3/checksums.txt)
    cp %s "$out"
    ;;
  *) echo "unexpected release URL: $url" >&2; exit 1 ;;
esac
`, shellSingleQuote(logPath), archiveCopy, shellSingleQuote(checksums))
	writeFakeBin(t, dir, "curl", body)
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

func TestBootstrapPipeModeBindsEveryDownloadToOneExactRelease(t *testing.T) {
	dir := binDir(t)
	installFakeHarness(t, dir, "claude")
	archive := filepath.Join(t.TempDir(), "hand-linux-amd64.tar.gz")
	writeHandArchive(t, archive)
	checksums := filepath.Join(t.TempDir(), "checksums.txt")
	if err := os.WriteFile(checksums, []byte(sha256Path(t, archive)+"  hand-linux-amd64.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "curl.log")
	installReleaseCurl(t, dir, archive, checksums, logPath, false)

	home := filepath.Join(t.TempDir(), "unicode-home-é")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	fleet := filepath.Join(home, "secondhand fleet 日本")
	got := runBootstrap(t, home, nil, "--fleet", fleet)
	if got.code != 0 {
		t.Fatalf("exit = %d (stderr %q)", got.code, got.stderr)
	}
	if !isFleetHome(fleet) {
		t.Fatalf("%s was not initialized", fleet)
	}
	urls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	wantURLs := "https://github.com/atqamz/hand/releases/download/v1.2.3/hand-linux-amd64.tar.gz\nhttps://github.com/atqamz/hand/releases/download/v1.2.3/checksums.txt\n"
	if string(urls) != wantURLs {
		t.Fatalf("curl URLs = %q, want %q", urls, wantURLs)
	}
}

func TestBootstrapRejectsAnInterruptedOrMismatchedReleaseDownload(t *testing.T) {
	for _, test := range []struct {
		name        string
		interrupted bool
		checksums   string
		want        string
	}{
		{name: "interrupted", interrupted: true, checksums: "unused", want: "download failed"},
		{name: "checksum mismatch", checksums: strings.Repeat("0", 64) + "  hand-linux-amd64.tar.gz\n", want: "checksum mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := binDir(t)
			archive := filepath.Join(t.TempDir(), "hand-linux-amd64.tar.gz")
			writeHandArchive(t, archive)
			checksums := filepath.Join(t.TempDir(), "checksums.txt")
			checksumData := test.checksums
			if checksumData == "unused" {
				checksumData = sha256Path(t, archive) + "  hand-linux-amd64.tar.gz\n"
			}
			if err := os.WriteFile(checksums, []byte(checksumData), 0o644); err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(t.TempDir(), "curl.log")
			installReleaseCurl(t, dir, archive, checksums, logPath, test.interrupted)
			home := t.TempDir()
			got := runBootstrap(t, home, nil, "--fleet", filepath.Join(home, "fleet"))
			if got.code == 0 || !strings.Contains(got.stderr, test.want) {
				t.Fatalf("exit = %d, stderr = %q, want %q", got.code, got.stderr, test.want)
			}
			if _, err := os.Stat(filepath.Join(home, ".local", "bin", "hand")); !os.IsNotExist(err) {
				t.Fatalf("failed download left an installed hand at %s", filepath.Join(home, ".local", "bin", "hand"))
			}
		})
	}
}

func TestBootstrapReportsAnInstallTargetFailureWithoutUsingSudo(t *testing.T) {
	dir := binDir(t)
	archive := filepath.Join(t.TempDir(), "hand-linux-amd64.tar.gz")
	writeHandArchive(t, archive)
	checksums := filepath.Join(t.TempDir(), "checksums.txt")
	if err := os.WriteFile(checksums, []byte(sha256Path(t, archive)+"  hand-linux-amd64.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "curl.log")
	installReleaseCurl(t, dir, archive, checksums, logPath, false)
	home := t.TempDir()
	installTarget := filepath.Join(home, "not-a-directory")
	if err := os.WriteFile(installTarget, []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runBootstrap(t, home, []string{"HAND_INSTALL_DIR=" + installTarget}, "--fleet", filepath.Join(home, "fleet"))
	if got.code == 0 {
		t.Fatalf("exit = 0, stderr = %q", got.stderr)
	}
	if !strings.Contains(got.stderr, "without sudo") {
		t.Fatalf("stderr = %q, want an explicit no-sudo recovery message", got.stderr)
	}
	if strings.Contains(got.stderr, "sudo mkdir") || strings.Contains(got.stderr, "sudo install") {
		t.Fatalf("stderr = %q, must not recommend a sudo command", got.stderr)
	}
}
