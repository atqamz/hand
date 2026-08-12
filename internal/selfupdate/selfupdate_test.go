package selfupdate

import (
	"archive/tar"
	"archive/zip"
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

func TestAssetName(t *testing.T) {
	tests := []struct {
		goos, goarch string
		want         string
	}{
		{goos: "linux", goarch: "amd64", want: "hand-linux-amd64.tar.gz"},
		{goos: "linux", goarch: "arm64", want: "hand-linux-arm64.tar.gz"},
		{goos: "darwin", goarch: "amd64", want: "hand-darwin-amd64.tar.gz"},
		{goos: "darwin", goarch: "arm64", want: "hand-darwin-arm64.tar.gz"},
		{goos: "windows", goarch: "amd64", want: "hand-windows-amd64.zip"},
	}
	for _, tt := range tests {
		if got := assetName(tt.goos, tt.goarch); got != tt.want {
			t.Errorf("assetName(%q, %q) = %q, want %q", tt.goos, tt.goarch, got, tt.want)
		}
	}
}

func TestAssetNameUsesRuntimePlatform(t *testing.T) {
	if got, want := AssetName(), assetName(runtime.GOOS, runtime.GOARCH); got != want {
		t.Fatalf("AssetName() = %q, want %q", got, want)
	}
}

func TestArchiveBinaryName(t *testing.T) {
	for _, tt := range []struct {
		goos, want string
	}{
		{goos: "linux", want: "hand"},
		{goos: "darwin", want: "hand"},
		{goos: "windows", want: "hand.exe"},
	} {
		if got := archiveBinaryName(tt.goos); got != tt.want {
			t.Errorf("archiveBinaryName(%q) = %q, want %q", tt.goos, got, tt.want)
		}
	}
}

// Fakes the two gh calls an update makes, mirroring where real gh puts its output: the
// extracted field alone on stdout, download progress on stderr, and a nonzero exit with
// the reason on stderr for a failure. runGH reads stdout only, so that split matters.
func writeFakeGH(t *testing.T, tag, fixtureDir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake gh is a POSIX shell script, not supported on windows")
	}
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
	archivePath := filepath.Join(dir, assetName)
	entry := archiveEntry{name: archiveBinaryName(runtime.GOOS), content: binaryContent, mode: 0o755}
	if runtime.GOOS == "windows" {
		writeZip(t, archivePath, []archiveEntry{entry})
	} else {
		writeTarGz(t, archivePath, []archiveEntry{entry})
	}

	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archiveBytes)
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

func TestApplyLeavesBinaryUnchangedOnChecksumMismatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake gh is a POSIX shell script, not supported on windows")
	}
	fixture := t.TempDir()
	assetName := AssetName()
	if err := os.WriteFile(filepath.Join(fixture, assetName), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "checksums.txt"), []byte("deadbeef  "+assetName+"\n"), 0o644); err != nil {
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
	t.Cleanup(func() { ExecutableOverride = restore })

	if err := Apply("atqamz/hand", "v0.5.0"); err == nil {
		t.Fatal("want checksum mismatch")
	}
	got, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old binary contents" {
		t.Fatalf("got %q, want the installed binary unchanged", got)
	}
	entries, err := os.ReadDir(execDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d install-directory entries, want only canonical executable", len(entries))
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

	if err := extractBinary(archivePath, binaryName, filepath.Join(dir, "out")); err == nil {
		t.Fatal("want error when binary missing from archive")
	}
}

func TestExtractBinaryFromTarGz(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "archive.tar.gz")
	writeTarGz(t, archivePath, []archiveEntry{
		{name: "other-before", content: []byte("before"), mode: 0o644},
		{name: "nested/hand", content: []byte("tar contents"), mode: 0o755},
		{name: "other-after", content: []byte("after"), mode: 0o644},
	})

	outPath := filepath.Join(dir, "out")
	if err := extractBinary(archivePath, "hand", outPath); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "tar contents" {
		t.Fatalf("got %q, want tar contents", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(outPath)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o755 {
			t.Fatalf("got mode %o, want 0755", got)
		}
	}
}

func TestExtractBinaryFromZip(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "archive.zip")
	writeZip(t, archivePath, []archiveEntry{
		{name: "other-before", content: []byte("before"), mode: 0o644},
		{name: "nested/hand.exe", content: []byte("zip contents"), mode: 0o755},
		{name: "other-after", content: []byte("after"), mode: 0o644},
	})

	outPath := filepath.Join(dir, "out")
	if err := extractBinary(archivePath, "hand.exe", outPath); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "zip contents" {
		t.Fatalf("got %q, want zip contents", got)
	}
}

func TestExtractBinaryRejectsCorruptGzip(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "archive.tar.gz")
	if err := os.WriteFile(archivePath, []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := extractBinary(archivePath, "hand", filepath.Join(dir, "out")); err == nil {
		t.Fatal("want corrupt gzip error")
	}
}

func TestExtractBinaryRejectsCorruptZip(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "archive.zip")
	if err := os.WriteFile(archivePath, []byte("not zip"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := extractBinary(archivePath, "hand.exe", filepath.Join(dir, "out")); err == nil {
		t.Fatal("want corrupt zip error")
	}
}

func TestExtractBinaryRejectsUnsupportedArchive(t *testing.T) {
	if err := extractBinary("archive.tar", "hand", filepath.Join(t.TempDir(), "out")); err == nil {
		t.Fatal("want unsupported archive error")
	}
}

type archiveEntry struct {
	name    string
	content []byte
	mode    int64
}

func writeTarGz(t *testing.T, path string, entries []archiveEntry) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(entry.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeZip(t *testing.T, path string, entries []archiveEntry) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(os.FileMode(entry.mode))
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(entry.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
