package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const releaseCommit = "0123456789abcdef0123456789abcdef01234567"

func TestPrepareReleaseBindsEveryAssetToOneExactRelease(t *testing.T) {
	output := t.TempDir()
	assets := []string{"hand-linux-amd64.tar.gz", "hand-linux-arm64.tar.gz", "hand-darwin-amd64.tar.gz", "hand-darwin-arm64.tar.gz", "hand-windows-amd64.zip"}
	for _, name := range assets {
		if err := os.WriteFile(filepath.Join(output, name), []byte("fixture "+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	runPrepareRelease(t, output, "v1.2.3", "1.2.3", releaseCommit)
	bootstrap, err := os.Stat(filepath.Join(output, "bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap.Mode()&0o111 == 0 {
		t.Fatal("generated bootstrap.sh is not executable")
	}

	var manifest struct {
		Tag       string   `json:"tag"`
		Version   string   `json:"version"`
		Commit    string   `json:"commit"`
		RuntimeID string   `json:"runtime_id"`
		Assets    []string `json:"assets"`
	}
	manifestData, err := os.ReadFile(filepath.Join(output, "release-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Tag != "v1.2.3" || manifest.Version != "1.2.3" || manifest.Commit != releaseCommit || manifest.RuntimeID != "rd2343fe130ff5ba2" {
		t.Fatalf("manifest = %+v, want release binding", manifest)
	}

	checksums, err := os.ReadFile(filepath.Join(output, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range append(assets, "bootstrap.sh", "bootstrap.ps1", "release-manifest.json") {
		want := checksumLine(t, filepath.Join(output, name), name)
		if !strings.Contains(string(checksums), want) {
			t.Fatalf("checksums.txt = %q, want %q", checksums, want)
		}
	}

	sh := exec.Command("sh", "-n", filepath.Join(output, "bootstrap.sh"))
	if out, err := sh.CombinedOutput(); err != nil {
		t.Fatalf("sh -n bootstrap.sh: %v: %s", err, out)
	}
}

func TestPrepareReleaseRejectsAReleaseBindingMismatch(t *testing.T) {
	output := t.TempDir()
	if err := os.WriteFile(filepath.Join(output, "hand-linux-amd64.tar.gz"), []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sh", filepath.Join(repoRoot(t), ".github", "scripts", "prepare-release.sh"), "release-1.2.3", "1.2.3", releaseCommit, output)
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("prepare-release.sh succeeded; output = %s", out)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() == 0 {
		t.Fatalf("prepare-release.sh error = %v, output = %s", err, out)
	}
	if !strings.Contains(string(out), "tag") {
		t.Fatalf("prepare-release.sh output = %q, want a tag validation error", out)
	}
}

func runPrepareRelease(t *testing.T, output, tag, version, commit string) {
	t.Helper()
	cmd := exec.Command("sh", filepath.Join(repoRoot(t), ".github", "scripts", "prepare-release.sh"), tag, version, commit, output)
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("prepare-release.sh: %v: %s", err, out)
	}
}

func checksumLine(t *testing.T, path, name string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]) + "  " + name
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}
