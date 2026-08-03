package cmd

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
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/selfupdate"
)

// writeFakeGHReleaseView fakes "release view --jq" as real gh's --jq flattens
// its JSON to the raw field value on stdout (selfupdate.go's runGH callers use
// --jq .tagName/.body), so a plain string with exit 0 is the faithful shape -
// no envelope to reproduce here, unlike herdr's call()/callVoid().
func writeFakeGHReleaseView(t *testing.T, tag string) {
	t.Helper()
	bin := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s' %q\n", tag)
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// writeFakeGHUpdate fakes the three gh invocations a full `hand update` makes:
// the tag lookup and the release notes lookup, both "release view --jq" in the
// shape writeFakeGHReleaseView documents above, plus the asset download - real
// `gh release download` leaves only the assets themselves in --dir and writes
// its progress to stderr, so copying the fixture in and printing nothing is the
// faithful success shape. The trailing arm mirrors real gh's failure shape too:
// a diagnostic on stderr and a non-zero exit, never a partial success.
func writeFakeGHUpdate(t *testing.T, tag, notes, fixtureDir string) {
	t.Helper()
	bin := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "release" ] && [ "$2" = "view" ] && [ "$3" = "--repo" ]; then
  printf '%%s' %q
  exit 0
fi
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
`, tag, notes, fixtureDir)
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func buildUpdateFixture(t *testing.T, binaryContent []byte) string {
	t.Helper()
	dir := t.TempDir()
	assetName := selfupdate.AssetName()

	var tarBuf bytes.Buffer
	gz := gzip.NewWriter(&tarBuf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "hand", Mode: 0o755, Size: int64(len(binaryContent))}); err != nil {
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

func setFakeExecutable(t *testing.T) string {
	t.Helper()
	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "hand")
	if err := os.WriteFile(execPath, []byte("old binary contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := selfupdate.ExecutableOverride
	selfupdate.ExecutableOverride = func() (string, error) { return execPath, nil }
	t.Cleanup(func() { selfupdate.ExecutableOverride = restore })
	return execPath
}

func TestUpdateRefreshesWorkspaceAndReportsChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("update binary layout targets unix asset names")
	}
	setFakeExecutable(t)
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	fixture := buildUpdateFixture(t, []byte("new binary contents"))
	writeFakeGHUpdate(t, "v0.5.0", "fixed the frobnicator", fixture)

	cmd := newUpdateCmd("v0.1.0")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	want := "current: v0.1.0\nlatest:  v0.5.0\nupdated hand to v0.5.0\nupdated AGENTS.md template\nchanged:\nfixed the frobnicator\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	agentsMD, err := os.ReadFile(filepath.Join(home, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agentsMD), "## Workflow") {
		t.Fatalf("got %q, want AGENTS.md written with the workflow template", agentsMD)
	}
}

func TestUpdateRefreshesHandHomeRatherThanWorkingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("update binary layout targets unix asset names")
	}
	setFakeExecutable(t)
	fleetHome := t.TempDir()
	mkFleetDirs(t, fleetHome)
	t.Setenv("HAND_HOME", fleetHome)
	t.Chdir(t.TempDir())

	fixture := buildUpdateFixture(t, []byte("new binary contents"))
	writeFakeGHUpdate(t, "v0.5.0", "fixed the frobnicator", fixture)

	cmd := newUpdateCmd("v0.1.0")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	want := "current: v0.1.0\nlatest:  v0.5.0\nupdated hand to v0.5.0\nupdated AGENTS.md template\nchanged:\nfixed the frobnicator\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	agentsMD, err := os.ReadFile(filepath.Join(fleetHome, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agentsMD), "## Workflow") {
		t.Fatalf("got %q, want AGENTS.md written with the workflow template", agentsMD)
	}
}

func TestUpdateSkipsAgentsRefreshOutsideAFleetHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("update binary layout targets unix asset names")
	}
	setFakeExecutable(t)
	t.Setenv("HAND_HOME", "")
	home := t.TempDir()
	t.Chdir(home)

	fixture := buildUpdateFixture(t, []byte("new binary contents"))
	writeFakeGHUpdate(t, "v0.5.0", "fixed the frobnicator", fixture)

	cmd := newUpdateCmd("v0.1.0")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	want := "current: v0.1.0\nlatest:  v0.5.0\nupdated hand to v0.5.0\nchanged:\nfixed the frobnicator\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(home, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("got AGENTS.md written outside a fleet home, err=%v", err)
	}
}

// "No home here" is the silent skip; "HAND_HOME names something that is not a
// home" is a misconfiguration, and swallowing it would leave the operator with
// an unrefreshed AGENTS.md and nothing on stderr saying why.
func TestUpdateWarnsWhenHandHomeIsNotAFleetHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("update binary layout targets unix asset names")
	}
	setFakeExecutable(t)
	home := t.TempDir()
	t.Chdir(home)
	notAHome := t.TempDir()
	t.Setenv("HAND_HOME", notAHome)

	fixture := buildUpdateFixture(t, []byte("new binary contents"))
	writeFakeGHUpdate(t, "v0.5.0", "fixed the frobnicator", fixture)

	cmd := newUpdateCmd("v0.1.0")
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got %v, want a successful update despite the misconfigured HAND_HOME", err)
	}

	got := out.String()
	want := "current: v0.1.0\nlatest:  v0.5.0\nupdated hand to v0.5.0\nchanged:\nfixed the frobnicator\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if !strings.Contains(errOut.String(), "warning: refresh AGENTS.md:") || !strings.Contains(errOut.String(), notAHome) {
		t.Fatalf("got stderr %q, want a warning naming %q", errOut.String(), notAHome)
	}
}

func TestUpdateDegradesGracefullyWithoutReleaseNotes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("update binary layout targets unix asset names")
	}
	setFakeExecutable(t)
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	fixture := buildUpdateFixture(t, []byte("new binary contents"))
	writeFakeGHUpdate(t, "v0.5.0", "", fixture)

	cmd := newUpdateCmd("v0.1.0")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	want := "current: v0.1.0\nlatest:  v0.5.0\nupdated hand to v0.5.0\nupdated AGENTS.md template\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// The binary is already replaced before the refresh runs, so a refresh failure
// must not turn a successful update into a nonzero exit.
func TestUpdateReportsVersionsWhenAgentsRefreshFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("update binary layout targets unix asset names")
	}
	setFakeExecutable(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "AGENTS.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	mkFleetDirs(t, home)

	fixture := buildUpdateFixture(t, []byte("new binary contents"))
	writeFakeGHUpdate(t, "v0.5.0", "fixed the frobnicator", fixture)

	cmd := newUpdateCmd("v0.1.0")
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got %v, want a successful update despite the refresh failure", err)
	}

	got := out.String()
	want := "current: v0.1.0\nlatest:  v0.5.0\nupdated hand to v0.5.0\nchanged:\nfixed the frobnicator\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if !strings.Contains(errOut.String(), "warning: refresh AGENTS.md:") {
		t.Fatalf("got stderr %q, want a refresh warning", errOut.String())
	}
}

func TestUpdateCheckReportsAvailableUpdate(t *testing.T) {
	writeFakeGHReleaseView(t, "v0.5.0")

	cmd := newUpdateCmd("v0.1.0")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--check"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := "update available: v0.1.0 -> v0.5.0\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestUpdateCheckReportsUpToDate(t *testing.T) {
	writeFakeGHReleaseView(t, "v0.1.0")

	cmd := newUpdateCmd("v0.1.0")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--check"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := "hand v0.1.0 is up to date\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestUpdateWithoutCheckSkipsInstallWhenUpToDate(t *testing.T) {
	writeFakeGHReleaseView(t, "v0.1.0")

	cmd := newUpdateCmd("v0.1.0")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := "hand v0.1.0 is up to date\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestUpdateCheckReportsAvailableUpdateForDevBuild(t *testing.T) {
	writeFakeGHReleaseView(t, "v0.5.0")

	cmd := newUpdateCmd("dev")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--check"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := "update available: dev -> v0.5.0\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestUpdatePropagatesLatestTagError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	cmd := newUpdateCmd("v0.1.0")
	cmd.SetArgs([]string{"--check"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("want error when gh is unreachable")
	}
}
