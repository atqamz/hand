package release

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// A platform tarball must carry only its native binary and metadata, a meta tarball
// only the launcher and its library - no secret, build path, or (lib.test.js living
// outside bin/) test artifact.
func TestNpmPackContainsExactlyTheExpectedFiles(t *testing.T) {
	requireTools(t, "jq", "git", "npm")
	fixture := copyNpmPackagingFixture(t)
	generateReal(t, fixture, "0.0.0-packproof")

	for _, tt := range []struct {
		dir  string
		want []string
	}{
		{"meta", []string{"bin/hand.js", "bin/lib.js", "package.json"}},
		{"platforms/linux-x64", []string{"bin/hand", "package.json"}},
		{"platforms/win32-x64", []string{"bin/hand.exe", "package.json"}},
	} {
		t.Run(tt.dir, func(t *testing.T) {
			got := npmPackFiles(t, filepath.Join(fixture.dir, "packaging", "npm", tt.dir))
			sort.Strings(got)
			want := append([]string(nil), tt.want...)
			sort.Strings(want)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("%s pack files = %v, want exactly %v", tt.dir, got, want)
			}
		})
	}
}

// Installs the meta package and its matching platform package from local tarballs only,
// proving real package resolution end to end on whichever runtime-qualified target this
// host natively is; skips elsewhere, like the workflow's own per-target native checks.
func TestNpmInstallFromLocalTarballsRunsTheMatchingNativeBinary(t *testing.T) {
	requireTools(t, "jq", "git", "npm", "node")
	npmKey, ok := map[string]string{"linux/amd64": "linux-x64", "windows/amd64": "win32-x64"}[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		t.Skipf("no runtime-qualified npm target matches this host (%s/%s)", runtime.GOOS, runtime.GOARCH)
	}
	fixture := copyNpmPackagingFixture(t)
	const version = "0.0.0-installproof"
	generateReal(t, fixture, version)

	metaTarball := npmPackTarball(t, filepath.Join(fixture.dir, "packaging", "npm", "meta"))
	platformTarball := npmPackTarball(t, filepath.Join(fixture.dir, "packaging", "npm", "platforms", npmKey))

	project := t.TempDir()
	install := exec.Command("npm", "install", "--no-save", "--no-audit", "--no-fund", metaTarball, platformTarball)
	install.Dir = project
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("npm install: %v: %s", err, out)
	}

	launcher := filepath.Join(project, "node_modules", "@atqamz", "hand", "bin", "hand.js")
	out, err := exec.Command("node", launcher, "build-info").CombinedOutput()
	if err != nil {
		t.Fatalf("installed launcher build-info: %v: %s", err, out)
	}
	for _, want := range []string{"version: " + version, "channel: stable", "commit: " + fixture.commit, "distribution: npm"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("build-info output = %q, want it to contain %q", out, want)
		}
	}

	// spawnSync forwards both argv and the child's exit code: an invalid subcommand
	// must fail through the launcher exactly as it would running the binary directly,
	// not be swallowed into a success or a generic launcher-level error.
	badCmd, badErr := exec.Command("node", launcher, "definitely-not-a-real-subcommand").CombinedOutput()
	if badErr == nil {
		t.Fatalf("installed launcher exited 0 for an invalid subcommand; output = %s", badCmd)
	}

	// A platform's optional dependency listed but not actually installed (--no-optional,
	// or an npm resolution that skipped it) is a distinct failure from an unpublished
	// platform, and must name the exact reinstall command rather than crash.
	metaOnly := t.TempDir()
	metaOnlyInstall := exec.Command("npm", "install", "--no-save", "--no-audit", "--no-fund", "--no-optional", metaTarball)
	metaOnlyInstall.Dir = metaOnly
	if out, err := metaOnlyInstall.CombinedOutput(); err != nil {
		t.Fatalf("npm install (meta only): %v: %s", err, out)
	}
	metaOnlyLauncher := filepath.Join(metaOnly, "node_modules", "@atqamz", "hand", "bin", "hand.js")
	refusal, err := exec.Command("node", metaOnlyLauncher, "build-info").CombinedOutput()
	if err == nil {
		t.Fatalf("launcher succeeded with no platform package installed; output = %s", refusal)
	}
	if !strings.Contains(string(refusal), "npm install -g @atqamz/hand") {
		t.Fatalf("refusal output = %q, want it to name the reinstall command", refusal)
	}
}

// Neither shipped launcher file references any network-capable primitive, so "nothing
// downloads at runtime" holds by construction rather than by a network test double.
func TestNpmLauncherSourceNeverReferencesNetworkIO(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"bin/hand.js", "bin/lib.js"} {
		path := filepath.Join(root, "packaging", "npm", "meta", name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, banned := range []string{"http.", "https.", "fetch(", "net.", "dns.", "XMLHttpRequest"} {
			if strings.Contains(string(data), banned) {
				t.Fatalf("%s references %q, want the npm launcher to never touch the network", name, banned)
			}
		}
	}
}

func generateReal(t *testing.T, fixture npmFixture, version string) {
	t.Helper()
	script := filepath.Join(fixture.dir, "packaging", "npm", "generate.sh")
	cmd := exec.Command("bash", script, "--version", version, "--commit", fixture.commit)
	cmd.Dir = fixture.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate.sh: %v: %s", err, out)
	}
}

func npmPackFiles(t *testing.T, dir string) []string {
	t.Helper()
	dest := t.TempDir()
	cmd := exec.Command("npm", "pack", "--json", "--pack-destination", dest)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("npm pack --json in %s: %v: %s", dir, err, out)
	}
	var packed []struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(out, &packed); err != nil {
		t.Fatalf("decode npm pack --json output %q: %v", out, err)
	}
	if len(packed) != 1 {
		t.Fatalf("npm pack --json produced %d entries, want exactly 1", len(packed))
	}
	files := make([]string, len(packed[0].Files))
	for i, f := range packed[0].Files {
		files[i] = f.Path
	}
	return files
}

func npmPackTarball(t *testing.T, dir string) string {
	t.Helper()
	dest := t.TempDir()
	cmd := exec.Command("npm", "pack", "--json", "--pack-destination", dest)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("npm pack --json in %s: %v: %s", dir, err, out)
	}
	var packed []struct {
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal(out, &packed); err != nil {
		t.Fatalf("decode npm pack --json output %q: %v", out, err)
	}
	if len(packed) != 1 {
		t.Fatalf("npm pack --json produced %d entries, want exactly 1", len(packed))
	}
	return filepath.Join(dest, packed[0].Filename)
}
