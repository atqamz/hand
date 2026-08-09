package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.4.0", "v0.3.1", true},
		{"v0.3.1", "v0.4.0", false},
		{"v0.3.1", "v0.3.1", false},
		{"v1.0.0", "v0.9.9", true},
		{"v0.1.2", "v0.1.1", true},
		{"v0.4.0", "dev", true},
		{"0.4.0", "0.3.1", true},
	}
	for _, c := range cases {
		got, err := IsNewer(c.latest, c.current)
		if err != nil {
			t.Fatalf("IsNewer(%q, %q): %v", c.latest, c.current, err)
		}
		if got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestIsNewerRejectsInvalidLatest(t *testing.T) {
	if _, err := IsNewer("not-a-version", "v0.3.1"); err == nil {
		t.Fatal("want error for unparseable latest version")
	}
}

// Fakes the two gh calls an update makes, mirroring where real gh puts its output: the
// extracted field alone on stdout, download progress on stderr, and a nonzero exit with
// the reason on stderr for a failure. runGH reads stdout only, so that split matters.
func writeFakeGH(t *testing.T, tag, fixtureDir string) {
	t.Helper()
	bin := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "release" ] && [ "$2" = "view" ]; then
  printf '%%s' %q
  exit 0
fi
if [ "$1" = "release" ] && [ "$2" = "download" ]; then
  dir=""
  prev=""
  for a in "$@"; do
    if [ "$prev" = "--dir" ]; then dir="$a"; fi
    prev="$a"
  done
  cp %q/* "$dir"/
  exit 0
fi
echo "unexpected gh invocation: $@" >&2
exit 1
`, tag, fixtureDir)
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func buildFixture(t *testing.T, binaryContent []byte) string {
	t.Helper()
	dir := t.TempDir()
	assetName := AssetName()

	var tarBuf bytes.Buffer
	gz := gzip.NewWriter(&tarBuf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: binaryName, Mode: 0o755, Size: int64(len(binaryContent))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(binaryContent); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, assetName), tarBuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256(tarBuf.Bytes())
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), assetName)
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(checksums), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLatestTag(t *testing.T) {
	writeFakeGH(t, "v0.5.0", t.TempDir())
	tag, err := LatestTag("atqamz/hand")
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v0.5.0" {
		t.Fatalf("got %q, want v0.5.0", tag)
	}
}

func TestApplyReplacesRunningBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("update binary layout targets unix asset names")
	}

	fixture := buildFixture(t, []byte("new binary contents"))
	writeFakeGH(t, "v0.5.0", fixture)

	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "hand")
	if err := os.WriteFile(execPath, []byte("old binary contents"), 0o755); err != nil {
		t.Fatal(err)
	}

	restore := ExecutableOverride
	ExecutableOverride = func() (string, error) { return execPath, nil }
	defer func() { ExecutableOverride = restore }()

	if err := Apply("atqamz/hand", "v0.5.0"); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new binary contents" {
		t.Fatalf("got %q, want new binary contents", got)
	}

	info, err := os.Stat(execPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("got mode %v, want 0755", info.Mode().Perm())
	}

	entries, err := os.ReadDir(execDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries in install dir, want only the replaced binary", len(entries))
	}
}

func TestApplyLeavesNoStagedFileWhenExtractionFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("update binary layout targets unix asset names")
	}

	fixture := t.TempDir()
	assetName := AssetName()
	payload := []byte("not a gzip stream")
	if err := os.WriteFile(filepath.Join(fixture, assetName), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), assetName)
	if err := os.WriteFile(filepath.Join(fixture, "checksums.txt"), []byte(checksums), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFakeGH(t, "v0.5.0", fixture)

	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "hand")
	if err := os.WriteFile(execPath, []byte("old binary contents"), 0o755); err != nil {
		t.Fatal(err)
	}

	restore := ExecutableOverride
	ExecutableOverride = func() (string, error) { return execPath, nil }
	defer func() { ExecutableOverride = restore }()

	if err := Apply("atqamz/hand", "v0.5.0"); err == nil {
		t.Fatal("want error when the asset is not a valid archive")
	}

	entries, err := os.ReadDir(execDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries in install dir, want no staged leftovers", len(entries))
	}
	got, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old binary contents" {
		t.Fatalf("got %q, want the running binary left untouched", got)
	}
}

func TestVerifyChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	assetName := AssetName()
	if err := os.WriteFile(filepath.Join(dir, assetName), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte("deadbeef  "+assetName+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksum(dir, assetName); err == nil {
		t.Fatal("want checksum mismatch error")
	}
}

func TestExtractBinaryMissingFromArchive(t *testing.T) {
	dir := t.TempDir()
	var tarBuf bytes.Buffer
	gz := gzip.NewWriter(&tarBuf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "other-file", Mode: 0o644, Size: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(dir, "archive.tar.gz")
	if err := os.WriteFile(archivePath, tarBuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := extractBinary(archivePath, filepath.Join(dir, "out")); err == nil {
		t.Fatal("want error when binary missing from archive")
	}
}
