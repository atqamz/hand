// Package npmpublish drives .github/scripts/npm-registry-check.sh and
// npm-publish-target.sh - the Phase 2/3 state machine atqamz/hand#283 requires -
// against a fake npm built once per process, so every distinguishable registry
// outcome the issue enumerates is reachable without a real registry write.
package npmpublish

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

const repoURL = "git+https://github.com/atqamz/hand.git"

func TestRegistryCheckDistinguishesEveryEnumeratedOutcome(t *testing.T) {
	script := scriptPath(t, "npm-registry-check.sh")

	for _, tt := range []struct {
		name        string
		seed        func(t *testing.T, state string)
		integrity   string
		wantOutcome string
		wantExit    int
	}{
		{
			name:        "package name has never published any version",
			seed:        func(t *testing.T, state string) {},
			integrity:   "sha512-anything",
			wantOutcome: "absent-new-package",
			wantExit:    0,
		},
		{
			name: "name exists, exact version absent, ownership matches",
			seed: func(t *testing.T, state string) {
				seedPackage(t, state, "@atqamz/hand-linux-x64", repoURL, map[string]string{"0.6.0": "sha512-old"})
			},
			integrity:   "sha512-new",
			wantOutcome: "absent-new-version",
			wantExit:    0,
		},
		{
			name: "name exists, exact version absent, ownership does not match",
			seed: func(t *testing.T, state string) {
				seedPackage(t, state, "@atqamz/hand-linux-x64", "git+https://github.com/someone-else/hijack.git", map[string]string{"0.6.0": "sha512-old"})
			},
			integrity:   "sha512-new",
			wantOutcome: "unexpected-ownership",
			wantExit:    1,
		},
		{
			name: "exact version present and matching",
			seed: func(t *testing.T, state string) {
				seedPackage(t, state, "@atqamz/hand-linux-x64", repoURL, map[string]string{"0.7.0": "sha512-match"})
			},
			integrity:   "sha512-match",
			wantOutcome: "verified-published",
			wantExit:    0,
		},
		{
			name: "exact version present, integrity mismatch",
			seed: func(t *testing.T, state string) {
				seedPackage(t, state, "@atqamz/hand-linux-x64", repoURL, map[string]string{"0.7.0": "sha512-registry"})
			},
			integrity:   "sha512-local",
			wantOutcome: "integrity-mismatch",
			wantExit:    1,
		},
		{
			name: "exact version present, integrity matches but repository does not",
			seed: func(t *testing.T, state string) {
				seedPackage(t, state, "@atqamz/hand-linux-x64", "git+https://github.com/someone-else/hijack.git", map[string]string{"0.7.0": "sha512-match"})
			},
			integrity:   "sha512-match",
			wantOutcome: "unexpected-ownership",
			wantExit:    1,
		},
		{
			name: "registry answers something other than a clean hit or a shaped 404",
			seed: func(t *testing.T, state string) {
				mustWriteFile(t, filepath.Join(state, "force-ambiguous"), "")
			},
			integrity:   "sha512-anything",
			wantOutcome: "ambiguous",
			wantExit:    1,
		},
	} {
		// npm 12 - what release CI pins - wraps a successful `npm view --json` document
		// in an array where npm 11 printed it bare; reading only the bare shape aborted
		// the v0.7.1 npm publish before it published anything (atqamz/hand#511).
		for _, shape := range []string{"array", "object"} {
			t.Run(tt.name+"/"+shape+" view document", func(t *testing.T) {
				state := t.TempDir()
				tt.seed(t, state)
				cmd := exec.Command("bash", script, "@atqamz/hand-linux-x64", "0.7.0", tt.integrity, repoURL)
				cmd.Env = append(fakeNpmEnv(t, state), "FAKE_NPM_VIEW_SHAPE="+shape)
				out, err := cmd.CombinedOutput()
				exit := exitCode(t, err)
				if exit != tt.wantExit {
					t.Fatalf("exit = %d, want %d; output = %s", exit, tt.wantExit, out)
				}
				if got := firstLine(out); got != tt.wantOutcome {
					t.Fatalf("outcome = %q, want %q; full output = %s", got, tt.wantOutcome, out)
				}
			})
		}
	}
}

func TestPublishTargetTakesTheBootstrapPathOnlyForATrulyNewPackage(t *testing.T) {
	state := t.TempDir()
	tarball := seedTarball(t, "@atqamz/hand-linux-x64", "0.7.0", "sha512-fresh", repoURL)

	cmd := exec.Command("bash", scriptPath(t, "npm-publish-target.sh"),
		"@atqamz/hand-linux-x64", "0.7.0", tarball, "sha512-fresh", repoURL)
	cmd.Env = append(fakeNpmEnv(t, state), "NPM_BOOTSTRAP_TOKEN=bootstrap-secret-value")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("npm-publish-target.sh: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "bootstrap token") {
		t.Fatalf("output = %s, want it to name the bootstrap token path", out)
	}

	calls := readCallsLog(t, state)
	if !containsCall(calls, "whoami") {
		t.Fatalf("calls = %v, want a whoami identity check before publishing", calls)
	}
	published := onlyPublishCall(t, calls)
	if !strings.Contains(published, "provenance=true") {
		t.Fatalf("publish call = %q, want --provenance on the bootstrap path", published)
	}
	if !strings.Contains(published, "authtoken=true") {
		t.Fatalf("publish call = %q, want NODE_AUTH_TOKEN visible to the bootstrap publish only", published)
	}
}

func TestPublishTargetVerifiesTheBootstrapPathAuthenticated(t *testing.T) {
	state := t.TempDir()
	tarball := seedTarball(t, "@atqamz/hand-linux-x64", "0.7.0", "sha512-fresh", repoURL)

	cmd := exec.Command("bash", scriptPath(t, "npm-publish-target.sh"),
		"@atqamz/hand-linux-x64", "0.7.0", tarball, "sha512-fresh", repoURL)
	cmd.Env = append(fakeNpmEnv(t, state), "NPM_BOOTSTRAP_TOKEN=bootstrap-secret-value")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("npm-publish-target.sh: %v: %s", err, out)
	}

	// The bootstrap token used to live only inside the subshell running `npm publish`,
	// so post-publish verification ran unauthenticated (atqamz/hand#506): the
	// pre-publish view must stay unauthenticated, every view after it must not.
	views := callsWithPrefix(t, readCallsLog(t, state), "view")
	if len(views) < 2 {
		t.Fatalf("calls = %v, want at least a pre-publish and a post-publish view call", views)
	}
	if !strings.Contains(views[0], "authtoken=false") {
		t.Fatalf("pre-publish view call = %q, want it unauthenticated", views[0])
	}
	for _, v := range views[1:] {
		if !strings.Contains(v, "authtoken=true") {
			t.Fatalf("post-publish view call = %q, want it authenticated with the bootstrap token", v)
		}
	}
}

func TestPublishTargetRefusesANewPackageWithoutTheBootstrapToken(t *testing.T) {
	state := t.TempDir()
	tarball := seedTarball(t, "@atqamz/hand-linux-x64", "0.7.0", "sha512-fresh", repoURL)

	cmd := exec.Command("bash", scriptPath(t, "npm-publish-target.sh"),
		"@atqamz/hand-linux-x64", "0.7.0", tarball, "sha512-fresh", repoURL)
	cmd.Env = fakeNpmEnv(t, state) // no NPM_BOOTSTRAP_TOKEN
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("npm-publish-target.sh succeeded with no bootstrap token; output = %s", out)
	}
	if !strings.Contains(string(out), "NPM_BOOTSTRAP_TOKEN") {
		t.Fatalf("output = %s, want it to name the missing bootstrap token", out)
	}
	// The read-only classification still runs unauthenticated - the issue requires the
	// token to be read only once a write actually turns out to be needed - but no
	// write is ever attempted without it.
	if containsCall(readCallsLog(t, state), "publish") {
		t.Fatal("a missing bootstrap token must never reach npm publish")
	}
}

func TestPublishTargetTakesTheOIDCPathForAnExistingPackagesNewVersion(t *testing.T) {
	state := t.TempDir()
	seedPackage(t, state, "@atqamz/hand-linux-x64", repoURL, map[string]string{"0.6.0": "sha512-old"})
	tarball := seedTarball(t, "@atqamz/hand-linux-x64", "0.7.0", "sha512-new", repoURL)

	cmd := exec.Command("bash", scriptPath(t, "npm-publish-target.sh"),
		"@atqamz/hand-linux-x64", "0.7.0", tarball, "sha512-new", repoURL)
	cmd.Env = fakeNpmEnv(t, state) // no NPM_BOOTSTRAP_TOKEN needed
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("npm-publish-target.sh: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "Trusted Publishing OIDC") {
		t.Fatalf("output = %s, want it to name the OIDC path", out)
	}
	published := onlyPublishCall(t, readCallsLog(t, state))
	if strings.Contains(published, "provenance=true") {
		t.Fatalf("publish call = %q, want no --provenance on the OIDC path: it is automatic under Trusted Publishing", published)
	}
	if strings.Contains(published, "authtoken=true") {
		t.Fatalf("publish call = %q, want no NODE_AUTH_TOKEN on the OIDC path", published)
	}
}

func TestPublishTargetSkipsAnAlreadyVerifiedVersionWithoutPublishing(t *testing.T) {
	state := t.TempDir()
	seedPackage(t, state, "@atqamz/hand-linux-x64", repoURL, map[string]string{"0.7.0": "sha512-match"})
	tarball := seedTarball(t, "@atqamz/hand-linux-x64", "0.7.0", "sha512-match", repoURL)

	cmd := exec.Command("bash", scriptPath(t, "npm-publish-target.sh"),
		"@atqamz/hand-linux-x64", "0.7.0", tarball, "sha512-match", repoURL)
	cmd.Env = fakeNpmEnv(t, state)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("npm-publish-target.sh: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "skipping") {
		t.Fatalf("output = %s, want it to say it is skipping an already-verified version", out)
	}
	if containsCall(readCallsLog(t, state), "publish") {
		t.Fatal("a verified-published version must never reach npm publish")
	}
}

func TestPublishTargetStopsOnAnIntegrityMismatchWithoutPublishing(t *testing.T) {
	state := t.TempDir()
	seedPackage(t, state, "@atqamz/hand-linux-x64", repoURL, map[string]string{"0.7.0": "sha512-registry-has-this"})
	tarball := seedTarball(t, "@atqamz/hand-linux-x64", "0.7.0", "sha512-local-pack-has-this", repoURL)

	cmd := exec.Command("bash", scriptPath(t, "npm-publish-target.sh"),
		"@atqamz/hand-linux-x64", "0.7.0", tarball, "sha512-local-pack-has-this", repoURL)
	cmd.Env = fakeNpmEnv(t, state)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("npm-publish-target.sh succeeded despite an integrity mismatch; output = %s", out)
	}
	if !strings.Contains(string(out), "integrity") {
		t.Fatalf("output = %s, want it to name the integrity mismatch", out)
	}
	if containsCall(readCallsLog(t, state), "publish") {
		t.Fatal("an integrity mismatch must never reach npm publish")
	}
}

func TestPublishTargetRefusesWhenTheBootstrapTokenDoesNotAuthenticate(t *testing.T) {
	state := t.TempDir()
	mustWriteFile(t, filepath.Join(state, "whoami-fail"), "")
	tarball := seedTarball(t, "@atqamz/hand-linux-x64", "0.7.0", "sha512-fresh", repoURL)

	cmd := exec.Command("bash", scriptPath(t, "npm-publish-target.sh"),
		"@atqamz/hand-linux-x64", "0.7.0", tarball, "sha512-fresh", repoURL)
	cmd.Env = append(fakeNpmEnv(t, state), "NPM_BOOTSTRAP_TOKEN=bad-token")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("npm-publish-target.sh succeeded despite a failing npm whoami; output = %s", out)
	}
	if !strings.Contains(string(out), "identity check") {
		t.Fatalf("output = %s, want it to name the failed identity check", out)
	}
	if containsCall(readCallsLog(t, state), "publish") {
		t.Fatal("a bootstrap token that fails npm whoami must never reach npm publish")
	}
}

func TestPublishTargetRefusesOIDCPublicationBehindAShadowingAuthToken(t *testing.T) {
	state := t.TempDir()
	seedPackage(t, state, "@atqamz/hand-linux-x64", repoURL, map[string]string{"0.6.0": "sha512-old"})
	tarball := seedTarball(t, "@atqamz/hand-linux-x64", "0.7.0", "sha512-new", repoURL)
	userconfig := filepath.Join(t.TempDir(), ".npmrc")
	mustWriteFile(t, userconfig, "//registry.npmjs.org/:_authToken=${NODE_AUTH_TOKEN}\n")

	cmd := exec.Command("bash", scriptPath(t, "npm-publish-target.sh"),
		"@atqamz/hand-linux-x64", "0.7.0", tarball, "sha512-new", repoURL)
	cmd.Env = append(fakeNpmEnv(t, state), "NPM_CONFIG_USERCONFIG="+userconfig)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("npm-publish-target.sh succeeded with a classic auth token shadowing OIDC; output = %s", out)
	}
	if !strings.Contains(string(out), "shadow OIDC") {
		t.Fatalf("output = %s, want it to name the shadowing auth config", out)
	}
	if containsCall(readCallsLog(t, state), "publish") {
		t.Fatal("a shadowing classic auth token must never reach npm publish")
	}
}

// Writes fakenpm's on-disk package state directly (the same shape
// tests/npmpublish/fakenpm/main.go reads), so a test can arrange "this version is
// already published" without ever calling npm publish itself.
func seedPackage(t *testing.T, state, name, repositoryURL string, versions map[string]string) {
	t.Helper()
	entries := map[string]map[string]string{}
	for v, integrity := range versions {
		entries[v] = map[string]string{"integrity": integrity}
	}
	doc := struct {
		RepositoryURL string                       `json:"repository_url"`
		Versions      map[string]map[string]string `json:"versions"`
	}{RepositoryURL: repositoryURL, Versions: entries}
	data := mustMarshal(t, doc)
	path := filepath.Join(state, "pkg-"+strings.NewReplacer("/", "_", "@", "_").Replace(name)+".json")
	mustWriteFile(t, path, string(data))
}

// Creates a placeholder tarball (its bytes are never inspected by fakenpm) plus the
// manifest sidecar fakenpm's publish handler reads to learn what
// name/version/integrity/repository a `npm publish <tarball>` call should register.
func seedTarball(t *testing.T, name, version, integrity, repositoryURL string) string {
	t.Helper()
	dir := t.TempDir()
	tarball := filepath.Join(dir, "package.tgz")
	mustWriteFile(t, tarball, "fixture tarball, never inspected by fakenpm")
	manifest := struct {
		Name          string `json:"name"`
		Version       string `json:"version"`
		Integrity     string `json:"integrity"`
		RepositoryURL string `json:"repository_url"`
	}{name, version, integrity, repositoryURL}
	mustWriteFile(t, tarball+".manifest.json", string(mustMarshal(t, manifest)))
	return tarball
}

func readCallsLog(t *testing.T, state string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(state, "calls.log"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func containsCall(calls []string, word string) bool {
	for _, c := range calls {
		if strings.Contains(c, word) {
			return true
		}
	}
	return false
}

func callsWithPrefix(t *testing.T, calls []string, prefix string) []string {
	t.Helper()
	var matched []string
	for _, c := range calls {
		if strings.HasPrefix(c, prefix+" ") {
			matched = append(matched, c)
		}
	}
	return matched
}

func onlyPublishCall(t *testing.T, calls []string) string {
	t.Helper()
	var published []string
	for _, c := range calls {
		if strings.HasPrefix(c, "published ") {
			published = append(published, c)
		}
	}
	if len(published) != 1 {
		t.Fatalf("calls = %v, want exactly one publish call", calls)
	}
	return published[0]
}

func firstLine(out []byte) string {
	line, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(line)
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	t.Fatalf("run script: %v", err)
	return -1
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func scriptPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(repoRoot(t), ".github", "scripts", name)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(source)))
}

var fakeNpmBuild struct {
	sync.Once
	path string
	err  error
}

// Builds the fake npm helper once per test process and returns an environment that
// puts it on PATH ahead of any real npm, with its own private FAKE_NPM_STATE.
func fakeNpmEnv(t *testing.T, state string) []string {
	t.Helper()
	fakeNpmBuild.Do(func() {
		dir, err := os.MkdirTemp("", "hand-fakenpm-")
		if err != nil {
			fakeNpmBuild.err = err
			return
		}
		target := filepath.Join(dir, "npm")
		cmd := exec.Command("go", "build", "-o", target, "./tests/npmpublish/fakenpm")
		cmd.Dir = repoRoot(t)
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			fakeNpmBuild.err = fmt.Errorf("build fake npm: %w: %s", err, out)
			return
		}
		fakeNpmBuild.path = dir
	})
	if fakeNpmBuild.err != nil {
		t.Fatal(fakeNpmBuild.err)
	}
	env := append(os.Environ(), "FAKE_NPM_STATE="+state, "PATH="+fakeNpmBuild.path+string(os.PathListSeparator)+os.Getenv("PATH"))
	// A real npm on PATH must never be reachable ahead of the fake in these tests.
	return env
}
