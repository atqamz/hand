package release

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/toolchain"
)

// The drift guard: derives the expected npm target set independently, from the same
// runtime.lock.json the generator itself reads, rather than a second hardcoded list this
// test could fall out of sync with (docs/adr/npm-publishes-only-runtime-qualified-targets-behind-one-operator-gate.md).
func TestNpmGeneratePrintTargetsMatchesTheRuntimeLock(t *testing.T) {
	requireTools(t, "jq")
	lock, err := toolchain.LoadLock()
	if err != nil {
		t.Fatalf("load runtime lock: %v", err)
	}
	var want []string
	for target, entry := range lock.Targets {
		if entry.Unsupported != "" {
			continue
		}
		goos, goarch, ok := strings.Cut(target, "/")
		if !ok {
			t.Fatalf("runtime lock target %q is not GOOS/GOARCH", target)
		}
		npmKey, err := npmPlatformKey(goos, goarch)
		if err != nil {
			t.Fatalf("target %s: %v", target, err)
		}
		want = append(want, npmKey)
	}
	sort.Strings(want)
	if len(want) == 0 {
		t.Fatal("runtime lock has no runtime-qualified targets; nothing to compare against")
	}

	got := runGenerateJSONTargets(t, filepath.Join(repoRoot(t), "packaging", "npm", "generate.sh"), nil)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("generate.sh --print-targets = %v, want exactly the runtime-lock-qualified set %v", got, want)
	}
}

// Proves the target set is derived from runtime.lock.json at generation time, not read
// off any list committed in the generator itself: a synthetic lock changes the output
// with zero edits to generate.sh.
func TestNpmGenerateTargetsReactToLockChanges(t *testing.T) {
	requireTools(t, "jq")
	script := filepath.Join(repoRoot(t), "packaging", "npm", "generate.sh")

	for _, tt := range []struct {
		name string
		lock string
		want []string
	}{
		{
			name: "a regression and a new qualification both take effect",
			lock: `{"targets":{
				"linux/amd64":{"components":{}},
				"windows/amd64":{"unsupported":"regressed for this test"},
				"darwin/arm64":{"components":{}}
			}}`,
			want: []string{"darwin-arm64", "linux-x64"},
		},
		{
			name: "every known GOOS/GOARCH qualified at once",
			lock: `{"targets":{
				"linux/amd64":{"components":{}},
				"linux/arm64":{"components":{}},
				"darwin/amd64":{"components":{}},
				"darwin/arm64":{"components":{}},
				"windows/amd64":{"components":{}}
			}}`,
			// Canonical order is alphabetical by GOOS/GOARCH (darwin/amd64 < darwin/arm64 <
			// linux/amd64 < linux/arm64 < windows/amd64), not by the resulting npm key.
			want: []string{"darwin-x64", "darwin-arm64", "linux-x64", "linux-arm64", "win32-x64"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			lockPath := filepath.Join(t.TempDir(), "runtime.lock.json")
			if err := os.WriteFile(lockPath, []byte(tt.lock), 0o644); err != nil {
				t.Fatal(err)
			}
			got := runGenerateJSONTargets(t, script, []string{"HAND_NPM_RUNTIME_LOCK=" + lockPath})
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("generate.sh --print-targets = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNpmGenerateFailsClosedRatherThanGuessing(t *testing.T) {
	requireTools(t, "jq")
	script := filepath.Join(repoRoot(t), "packaging", "npm", "generate.sh")

	for _, tt := range []struct {
		name string
		lock string
		want string
	}{
		{
			name: "no runtime-qualified target at all",
			lock: `{"targets":{"linux/amd64":{"unsupported":"none qualified"}}}`,
			want: "no runtime-qualified targets",
		},
		{
			name: "a GOARCH npm has no os/cpu mapping for",
			lock: `{"targets":{"linux/riscv64":{"components":{}}}}`,
			want: "no npm cpu mapping",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			lockPath := filepath.Join(t.TempDir(), "runtime.lock.json")
			if err := os.WriteFile(lockPath, []byte(tt.lock), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", script, "--print-targets")
			cmd.Env = append(os.Environ(), "HAND_NPM_RUNTIME_LOCK="+lockPath)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("generate.sh --print-targets succeeded; output = %s, want a fail-closed refusal", out)
			}
			if !strings.Contains(string(out), tt.want) {
				t.Fatalf("generate.sh output = %q, want it to name %q", out, tt.want)
			}
		})
	}
}

// Runs the real generator end to end, go faked for speed (as executeWorkflowBuild fakes
// it for the release build job): qualified targets get built, an unsupported template is
// left alone, a stale optionalDependencies entry is dropped, a commit mismatch refuses.
func TestNpmGenerateBuildsExactlyTheDerivedTargets(t *testing.T) {
	requireTools(t, "jq", "git")
	fixture := copyNpmPackagingFixture(t)

	// A stale optionalDependencies entry from a target that has since regressed out of
	// the runtime-qualified set: proves the rewrite replaces the object instead of only
	// patching the value of whatever keys happen to already be present.
	metaPkg := filepath.Join(fixture.dir, "packaging", "npm", "meta", "package.json")
	rewriteJSONField(t, metaPkg, "optionalDependencies", map[string]string{
		"@atqamz/hand-darwin-arm64": "0.5.0",
	})

	fakeBin := t.TempDir()
	buildLog := filepath.Join(t.TempDir(), "go-build.log")
	installFakeGo(t, fakeBin, buildLog)

	script := filepath.Join(fixture.dir, "packaging", "npm", "generate.sh")
	const version = "0.7.0"

	// Refuses when the caller's claimed commit does not match the checkout it is
	// actually standing in - the same identity guard the release build job applies to
	// itself, applied here before any package.json is touched.
	cmd := exec.Command("bash", script, "--version", version, "--commit", strings.Repeat("a", 40))
	cmd.Dir = fixture.dir
	cmd.Env = envWithPath(fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("generate.sh succeeded against a mismatched --commit; output = %s", out)
	}
	if !strings.Contains(string(out), "does not match") {
		t.Fatalf("generate.sh output = %q, want a HEAD/--commit mismatch refusal", out)
	}
	if _, err := os.Stat(buildLog); err == nil {
		t.Fatal("generate.sh built a binary before verifying its identity guard")
	}

	cmd = exec.Command("bash", script, "--version", version, "--commit", fixture.commit)
	cmd.Dir = fixture.dir
	cmd.Env = envWithPath(fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate.sh: %v: %s", err, out)
	}

	wantBuilt := map[string]bool{"linux-x64": true, "win32-x64": true}
	for _, name := range []string{"linux-x64", "win32-x64", "darwin-x64", "darwin-arm64", "linux-arm64"} {
		platformDir := filepath.Join(fixture.dir, "packaging", "npm", "platforms", name)
		pkg := readJSON(t, filepath.Join(platformDir, "package.json"))
		binName := "hand"
		if name == "win32-x64" {
			binName = "hand.exe"
		}
		binPath := filepath.Join(platformDir, "bin", binName)
		_, statErr := os.Stat(binPath)
		built := statErr == nil
		if built != wantBuilt[name] {
			t.Fatalf("%s built = %v, want %v", name, built, wantBuilt[name])
		}
		if wantBuilt[name] {
			if pkg["version"] != version {
				t.Fatalf("%s package.json version = %v, want %s", name, pkg["version"], version)
			}
		} else if pkg["version"] == version {
			t.Fatalf("%s package.json was rewritten to the new version; want unsupported templates left alone", name)
		}
	}

	meta := readJSON(t, metaPkg)
	if meta["version"] != version {
		t.Fatalf("meta package.json version = %v, want %s", meta["version"], version)
	}
	optionalDeps, ok := meta["optionalDependencies"].(map[string]any)
	if !ok {
		t.Fatalf("meta package.json optionalDependencies = %#v, want an object", meta["optionalDependencies"])
	}
	wantDeps := map[string]string{"@atqamz/hand-linux-x64": version, "@atqamz/hand-win32-x64": version}
	if len(optionalDeps) != len(wantDeps) {
		t.Fatalf("optionalDependencies = %#v, want exactly %v", optionalDeps, wantDeps)
	}
	for dep, wantVersion := range wantDeps {
		if got := optionalDeps[dep]; got != wantVersion {
			t.Fatalf("optionalDependencies[%s] = %v, want %s", dep, got, wantVersion)
		}
	}
	if _, stale := optionalDeps["@atqamz/hand-darwin-arm64"]; stale {
		t.Fatal("optionalDependencies still references a target that regressed out of the runtime-qualified set")
	}
}

type npmFixture struct {
	dir    string
	commit string
}

// Copies every tracked file's current working-tree bytes into a fresh git repository, so
// generate.sh's HEAD-identity check and a real `go build .` both work as they do in the
// real repository, without a test mutating tracked files in place.
func copyNpmPackagingFixture(t *testing.T) npmFixture {
	t.Helper()
	root := repoRoot(t)
	dir := t.TempDir()
	for _, rel := range trackedFiles(t, root) {
		copyFile(t, filepath.Join(root, rel), filepath.Join(dir, rel))
	}

	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+writeScratchGitConfig(t),
		"GIT_CONFIG_NOSYSTEM=1",
	)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"commit", "-q", "-m", "fixture"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	commit := strings.TrimSpace(gitOutput(t, dir, env, "rev-parse", "HEAD"))
	return npmFixture{dir: dir, commit: commit}
}

func writeScratchGitConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gitconfig")
	content := "[user]\n\tname = test\n\temail = test@example.invalid\n[commit]\n\tgpgsign = false\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func gitOutput(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

// Stands in for the real Go toolchain: ignores the real source tree and writes a fixed
// placeholder binary, logging every invocation so a test can assert generate.sh never
// reached the build step when an earlier guard should have refused first.
func installFakeGo(t *testing.T, bin, logPath string) {
	t.Helper()
	script := "#!/bin/sh\nset -eu\n" +
		"printf 'go %s\\n' \"$*\" >> '" + logPath + "'\n" +
		"out=hand\n" +
		"while [ \"$#\" -gt 0 ]; do\n" +
		"  if [ \"$1\" = -o ]; then out=$2; shift 2; else shift; fi\n" +
		"done\n" +
		"mkdir -p \"$(dirname \"$out\")\"\n" +
		": > \"$out\"\n" +
		"chmod 755 \"$out\"\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Lists tracked files by name only (git ls-files), so the caller copies each one's
// current working-tree bytes - including an uncommitted edit - rather than git's own
// committed blob.
func trackedFiles(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var files []string
	for _, rel := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if rel != "" {
			files = append(files, rel)
		}
	}
	return files
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return value
}

func rewriteJSONField(t *testing.T, path, field string, value any) {
	t.Helper()
	doc := readJSON(t, path)
	doc[field] = value
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGenerateJSONTargets(t *testing.T, script string, extraEnv []string) []string {
	t.Helper()
	cmd := exec.Command("bash", script, "--print-targets")
	cmd.Env = append(os.Environ(), extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generate.sh --print-targets: %v: %s", err, out)
	}
	var targets []string
	if err := json.Unmarshal(out, &targets); err != nil {
		t.Fatalf("decode generate.sh --print-targets output %q: %v", out, err)
	}
	return targets
}

func requireTools(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s is not on PATH", name)
		}
	}
}

// Mirrors generate.sh's own npm_os/npm_cpu mapping independently, so the drift guard
// above compares two separate readings of the same runtime lock rather than the
// generator against itself.
func npmPlatformKey(goos, goarch string) (string, error) {
	npmOS, ok := map[string]string{"linux": "linux", "darwin": "darwin", "windows": "win32"}[goos]
	if !ok {
		return "", errors.New("no npm os mapping for GOOS " + goos)
	}
	npmCPU, ok := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	if !ok {
		return "", errors.New("no npm cpu mapping for GOARCH " + goarch)
	}
	return npmOS + "-" + npmCPU, nil
}
