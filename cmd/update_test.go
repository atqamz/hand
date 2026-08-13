package cmd

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
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/selfupdate"
)

// Fakes "release view --jq" as real gh's --jq flattens its JSON to the raw field value on stdout
// (selfupdate.go's runGH callers use --jq .tagName/.body), so a plain string with exit 0 is the faithful
// shape - no envelope to reproduce here, unlike herdr's call()/callVoid().
func writeFakeGHReleaseView(t *testing.T, tag string) {
	t.Helper()
	bin := faketool.Bin(t)
	faketool.GH{Release: faketool.GHRelease{Tag: tag}}.Install(t, bin)
}

// Fakes the three gh invocations a full `hand update` makes: the tag lookup and the release notes lookup,
// both "release view --jq" in the shape writeFakeGHReleaseView documents above, plus the asset download.
func writeFakeGHUpdate(t *testing.T, tag, notes, fixtureDir string) {
	t.Helper()
	bin := faketool.Bin(t)
	faketool.GH{Release: faketool.GHRelease{
		Tag: tag, Notes: notes, FixtureDir: fixtureDir,
	}}.Install(t, bin)
}

func buildUpdateFixture(t *testing.T, binaryContent []byte) string {
	t.Helper()
	dir := t.TempDir()
	assetName := selfupdate.AssetName()
	archivePath := filepath.Join(dir, assetName)
	if runtime.GOOS == "windows" {
		file, err := os.Create(archivePath)
		if err != nil {
			t.Fatal(err)
		}
		zw := zip.NewWriter(file)
		header := &zip.FileHeader{Name: "hand.exe", Method: zip.Deflate}
		header.SetMode(0o755)
		entry, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(binaryContent); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
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
		if err := os.WriteFile(archivePath, tarBuf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
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

func writeOwnedSessionHook(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"/old/path/hand"},{"type":"command","command":"/usr/bin/custom"}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Every outcome renders the same seven fields, so a reader parses one schema
// rather than a set of lines that appear or do not.
func appliedUpdateDoc(agentsMD, sessionHook string, notes ...string) string {
	doc := "current: v0.1.0\n" +
		"current_channel: stable\n" +
		"current_commit: unknown\n" +
		"latest: v0.5.0\n" +
		"latest_channel: stable\n" +
		"latest_commit: unknown\n" +
		"update_available: true\n" +
		"updated: true\n" +
		"agents_md: " + agentsMD + "\n" +
		"session_hook: " + sessionHook + "\n" +
		fmt.Sprintf("notes[%d]:\n", len(notes))
	for _, note := range notes {
		doc += "  - " + note + "\n"
	}
	return doc + "help[1]:\n" +
		"  - Run `hand doctor` to check this home's AGENTS.md against the template v0.5.0 installed\n"
}

func TestUpdateRefreshesWorkspaceAndReportsChanges(t *testing.T) {
	setFakeExecutable(t)
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeOwnedSessionHook(t, home)

	fixture := buildUpdateFixture(t, []byte("new binary contents"))
	writeFakeGHUpdate(t, "v0.5.0", "fixed the frobnicator", fixture)

	cmd := newUpdateCmd(stableBuild("v0.1.0"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if want := appliedUpdateDoc("refreshed", "removed", "fixed the frobnicator"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	agentsMD, err := os.ReadFile(filepath.Join(home, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agentsMD), "## Secondhand supervisor bootstrap") {
		t.Fatalf("got %q, want AGENTS.md written with the supervisor bootstrap", agentsMD)
	}
	settings, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(settings), "/old/path/hand") || !strings.Contains(string(settings), "/usr/bin/custom") {
		t.Fatalf("settings = %q, want only the unrelated hook preserved", settings)
	}
}

// The refreshed template directs the agent at data files a home initialized
// before them never had, so update seeds whichever are missing.
func TestUpdateSeedsDataSkeletonsMissingFromAnOlderHome(t *testing.T) {
	setFakeExecutable(t)
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	fixture := buildUpdateFixture(t, []byte("new binary contents"))
	writeFakeGHUpdate(t, "v0.5.0", "fixed the frobnicator", fixture)

	cmd := newUpdateCmd(stableBuild("v0.1.0"))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"data/backlog.md":      "# Backlog",
		"data/operator.md":     "## Hard constraints",
		"data/learnings.md":    "# Learnings",
		"data/done-archive.md": "# Done archive",
		"data/note-archive.md": "# Note archive",
	}
	for rel, header := range want {
		got, err := os.ReadFile(filepath.Join(home, rel))
		if err != nil {
			t.Fatalf("%s missing after update: %v", rel, err)
		}
		if !strings.Contains(string(got), header) {
			t.Fatalf("%s = %q, want it to contain %q", rel, got, header)
		}
	}
	if errOut.Len() != 0 {
		t.Fatalf("got stderr %q, want none", errOut.String())
	}
}

// A home whose data/ directory is gone still resolves as a home on its
// state/hand.db marker, so update has to create the directory it seeds into
// rather than warning about six files it could not write.
func TestUpdateRecreatesAMissingDataDirectory(t *testing.T) {
	setFakeExecutable(t)
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := os.RemoveAll(filepath.Join(home, "data")); err != nil {
		t.Fatal(err)
	}

	fixture := buildUpdateFixture(t, []byte("new binary contents"))
	writeFakeGHUpdate(t, "v0.5.0", "fixed the frobnicator", fixture)

	cmd := newUpdateCmd(stableBuild("v0.1.0"))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if errOut.Len() != 0 {
		t.Fatalf("got stderr %q, want none", errOut.String())
	}
	for _, rel := range []string{"data/backlog.md", "data/projects.md", "data/operator.md", "data/learnings.md", "data/done-archive.md", "data/note-archive.md"} {
		if _, err := os.Stat(filepath.Join(home, rel)); err != nil {
			t.Fatalf("%s missing after update: %v", rel, err)
		}
	}
}

func TestUpdateLeavesExistingOperatorContextAlone(t *testing.T) {
	setFakeExecutable(t)
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	existing := "# Operator\n\n## Authority\n\nMerge without asking.\n"
	if err := os.WriteFile(filepath.Join(home, "data", "operator.md"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	fixture := buildUpdateFixture(t, []byte("new binary contents"))
	writeFakeGHUpdate(t, "v0.5.0", "fixed the frobnicator", fixture)

	cmd := newUpdateCmd(stableBuild("v0.1.0"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(home, "data", "operator.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != existing {
		t.Fatalf("data/operator.md = %q, want unchanged %q", got, existing)
	}
}

func TestUpdateRefreshesHandHomeRatherThanWorkingDirectory(t *testing.T) {
	setFakeExecutable(t)
	fleetHome := t.TempDir()
	mkFleetDirs(t, fleetHome)
	writeOwnedSessionHook(t, fleetHome)
	t.Setenv("HAND_HOME", fleetHome)
	t.Chdir(t.TempDir())

	fixture := buildUpdateFixture(t, []byte("new binary contents"))
	writeFakeGHUpdate(t, "v0.5.0", "fixed the frobnicator", fixture)

	cmd := newUpdateCmd(stableBuild("v0.1.0"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if want := appliedUpdateDoc("refreshed", "removed", "fixed the frobnicator"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	agentsMD, err := os.ReadFile(filepath.Join(fleetHome, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agentsMD), "## Secondhand supervisor bootstrap") {
		t.Fatalf("got %q, want AGENTS.md written with the supervisor bootstrap", agentsMD)
	}
}

func TestUpdateSkipsAgentsRefreshOutsideAFleetHome(t *testing.T) {
	setFakeExecutable(t)
	t.Setenv("HAND_HOME", "")
	home := t.TempDir()
	t.Chdir(home)

	fixture := buildUpdateFixture(t, []byte("new binary contents"))
	writeFakeGHUpdate(t, "v0.5.0", "fixed the frobnicator", fixture)

	cmd := newUpdateCmd(stableBuild("v0.1.0"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if want := appliedUpdateDoc("no-fleet-home", "no-fleet-home", "fixed the frobnicator"); got != want {
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
	setFakeExecutable(t)
	home := t.TempDir()
	t.Chdir(home)
	notAHome := t.TempDir()
	t.Setenv("HAND_HOME", notAHome)

	fixture := buildUpdateFixture(t, []byte("new binary contents"))
	writeFakeGHUpdate(t, "v0.5.0", "fixed the frobnicator", fixture)

	cmd := newUpdateCmd(stableBuild("v0.1.0"))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got %v, want a successful update despite the misconfigured HAND_HOME", err)
	}

	got := out.String()
	if want := appliedUpdateDoc("failed", "no-fleet-home", "fixed the frobnicator"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	quotedHome := fmt.Sprintf("%q", notAHome)
	if !strings.Contains(errOut.String(), "warning: refresh AGENTS.md:") || !strings.Contains(errOut.String(), quotedHome) {
		t.Fatalf("got stderr %q, want a warning naming %q", errOut.String(), quotedHome)
	}
}

func TestUpdateDegradesGracefullyWithoutReleaseNotes(t *testing.T) {
	setFakeExecutable(t)
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	fixture := buildUpdateFixture(t, []byte("new binary contents"))
	writeFakeGHUpdate(t, "v0.5.0", "", fixture)

	cmd := newUpdateCmd(stableBuild("v0.1.0"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if want := appliedUpdateDoc("refreshed", "unchanged"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// The binary is already replaced before the refresh runs, so a refresh failure
// must not turn a successful update into a nonzero exit.
func TestUpdateReportsVersionsWhenAgentsRefreshFails(t *testing.T) {
	setFakeExecutable(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "AGENTS.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	mkFleetDirs(t, home)

	fixture := buildUpdateFixture(t, []byte("new binary contents"))
	writeFakeGHUpdate(t, "v0.5.0", "fixed the frobnicator", fixture)

	cmd := newUpdateCmd(stableBuild("v0.1.0"))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got %v, want a successful update despite the refresh failure", err)
	}

	got := out.String()
	if want := appliedUpdateDoc("failed", "unchanged", "fixed the frobnicator"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if !strings.Contains(errOut.String(), "warning: refresh AGENTS.md:") {
		t.Fatalf("got stderr %q, want a refresh warning", errOut.String())
	}
}

func TestUpdatePreservesOwnedSessionHookWhenAgentsRefreshFails(t *testing.T) {
	setFakeExecutable(t)
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	malformed := "<!-- hand:generated:start -->\n<!-- hand:generated:start -->\n<!-- hand:generated:end -->\n"
	if err := os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte(malformed), 0o644); err != nil {
		t.Fatal(err)
	}
	writeOwnedSessionHook(t, home)
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	before, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}

	fixture := buildUpdateFixture(t, []byte("new binary contents"))
	writeFakeGHUpdate(t, "v0.5.0", "fixed the frobnicator", fixture)

	cmd := newUpdateCmd(stableBuild("v0.1.0"))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got %v, want a successful binary update despite the refresh failure", err)
	}

	if want := appliedUpdateDoc("failed", "unchanged", "fixed the frobnicator"); out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
	if !strings.Contains(errOut.String(), "warning: refresh AGENTS.md:") {
		t.Fatalf("stderr = %q, want the refresh warning", errOut.String())
	}
	if strings.Contains(errOut.String(), "retire the session hook") {
		t.Fatalf("stderr = %q, want no retirement attempt after refresh failed", errOut.String())
	}
	after, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("settings changed after refresh failure:\n got %s\nwant %s", after, before)
	}
}

// The binary is already replaced before retirement runs, so malformed operator
// settings produce a warning and a failed field without changing update success.
func TestUpdateReportsVersionsWhenSessionHookRetirementFails(t *testing.T) {
	setFakeExecutable(t)
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	fixture := buildUpdateFixture(t, []byte("new binary contents"))
	writeFakeGHUpdate(t, "v0.5.0", "fixed the frobnicator", fixture)

	cmd := newUpdateCmd(stableBuild("v0.1.0"))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got %v, want a successful update despite hook retirement failure", err)
	}

	if want := appliedUpdateDoc("refreshed", "failed", "fixed the frobnicator"); out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
	if !strings.Contains(errOut.String(), "warning: retire the session hook:") {
		t.Fatalf("got stderr %q, want a retirement warning", errOut.String())
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "{not json" {
		t.Fatalf("settings = %q, %v, want invalid settings untouched", raw, err)
	}
}

func checkedUpdateDoc(current, latest string, available bool) string {
	currentChannel := "stable"
	if current == "dev" {
		currentChannel = "dev"
	}
	doc := "current: " + current + "\n" +
		"current_channel: " + currentChannel + "\n" +
		"current_commit: unknown\n" +
		"latest: " + latest + "\n" +
		"latest_channel: stable\n" +
		"latest_commit: unknown\n" +
		fmt.Sprintf("update_available: %t\n", available) +
		"updated: false\n" +
		"agents_md: not-applicable\n" +
		"session_hook: not-applicable\n" +
		"notes[0]:\n"
	if !available {
		return doc
	}
	return doc + "help[1]:\n" +
		"  - Run `hand update` to install " + latest + ", which also refreshes this home's AGENTS.md template\n"
}

func TestUpdateCheckReportsAvailableUpdate(t *testing.T) {
	writeFakeGHReleaseView(t, "v0.5.0")

	cmd := newUpdateCmd(stableBuild("v0.1.0"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--check"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := checkedUpdateDoc("v0.1.0", "v0.5.0", true)
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

// Up to date is an answer, not an absence: it renders the same schema as an
// available update, differing only in the value of update_available.
func TestUpdateCheckReportsUpToDate(t *testing.T) {
	writeFakeGHReleaseView(t, "v0.1.0")

	cmd := newUpdateCmd(stableBuild("v0.1.0"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--check"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := checkedUpdateDoc("v0.1.0", "v0.1.0", false)
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestUpdateWithoutCheckSkipsInstallWhenUpToDate(t *testing.T) {
	writeFakeGHReleaseView(t, "v0.1.0")

	cmd := newUpdateCmd(stableBuild("v0.1.0"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := checkedUpdateDoc("v0.1.0", "v0.1.0", false)
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestUpdateCheckReportsAvailableUpdateForDevBuild(t *testing.T) {
	writeFakeGHReleaseView(t, "v0.5.0")

	cmd := newUpdateCmd(devBuild("dev"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--check"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := checkedUpdateDoc("dev", "v0.5.0", true)
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestUpdatePropagatesLatestTagError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	cmd := newUpdateCmd(stableBuild("v0.1.0"))
	cmd.SetArgs([]string{"--check"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("want error when gh is unreachable")
	}
}
