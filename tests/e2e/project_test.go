//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/toolchain"
)

func TestProjectAddPreservesHTTPSWhenGitInsteadOfRoutesIt(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote")
	initGitRepo(t, remote)
	input := "https://github.com/owner/repo.git"
	redirectGitRemote(t, input, remote)
	t.Setenv("GIT_SSH_COMMAND", "false")

	dir := binDir(t)
	writeFakeTreehouse(t, dir, filepath.Join(t.TempDir(), "unused-worktree"))
	home := newHome(t)

	added := runHand(t, home, "project", "add", input, "--mode", "direct-pr")
	if added.code != 0 {
		t.Fatalf("project add: exit %d, stderr %q", added.code, added.stderr)
	}
	if strings.Contains(added.stdout, "Normalized HTTPS locator") {
		t.Fatalf("project add stdout = %q, want original HTTPS locator", added.stdout)
	}
	projects, err := project.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].URL != input {
		t.Fatalf("project.List = %+v, want URL %q", projects, input)
	}
	if got := runGitIn(t, filepath.Join(home, "projects", "repo"), "config", "--get", "remote.origin.url"); got != input+"\n" {
		t.Fatalf("clone origin = %q, want %q", got, input)
	}
}

func TestProjectAddNormalizesEffectiveHTTPSWithoutHelper(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote")
	initGitRepo(t, remote)
	input := "https://github.com/owner/repo.git"
	ssh := "git@github.com:owner/repo.git"
	redirectGitRemote(t, ssh, remote)

	dir := binDir(t)
	writeFakeTreehouse(t, dir, filepath.Join(t.TempDir(), "unused-worktree"))
	home := newHome(t)
	cloneLog := replaceManagedGit(t, home, input, input, "git: 'remote-https' is not a git command. See 'git --help'.")

	added := runHand(t, home, "project", "add", input, "--mode", "direct-pr")
	if added.code != 0 {
		t.Fatalf("project add: exit %d, stderr %q", added.code, added.stderr)
	}
	if !strings.Contains(added.stdout, "Normalized HTTPS locator") {
		t.Fatalf("project add stdout = %q, want visible fallback explanation", added.stdout)
	}
	clonePath := filepath.Join(home, "projects", "repo")
	if got := runGitIn(t, clonePath, "config", "--get", "remote.origin.url"); got != ssh+"\n" {
		t.Fatalf("clone origin = %q, want %q", got, ssh)
	}
	if got, err := os.ReadFile(cloneLog); err != nil || string(got) != ssh+"\n" {
		t.Fatalf("clone attempts = %q, %v; want SSH only", got, err)
	}
}

func TestProjectAddPreservesConfiguredSSHRouteDespiteMissingHTTPSStderr(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote")
	initGitRepo(t, remote)
	input := "https://github.com/owner/repo.git"
	ssh := "git@github.com:owner/repo.git"
	redirectGitRemote(t, ssh, remote)

	dir := binDir(t)
	writeFakeTreehouse(t, dir, filepath.Join(t.TempDir(), "unused-worktree"))
	home := newHome(t)
	cloneLog := replaceManagedGit(t, home, input, ssh,
		"git: 'remote-https' is not a git command. See 'git --help'.",
		"fatal: authentication failed",
	)

	added := runHand(t, home, "project", "add", input, "--mode", "direct-pr")
	if added.code == 0 || strings.Contains(added.stdout, "Normalized HTTPS locator") {
		t.Fatalf("project add = %+v, want original HTTPS clone failure without normalization", added)
	}
	if got, err := os.ReadFile(cloneLog); err != nil || string(got) != input+"\n" {
		t.Fatalf("clone attempts = %q, %v; want only original HTTPS", got, err)
	}
}

func TestProjectAddDoesNotRetryUnrecognizedHTTPSAfterMissingHelper(t *testing.T) {
	for _, input := range []string{
		"https://example.com/owner/repo.git",
		"https://github.com/owner/repo.git?ref=main",
	} {
		t.Run(input, func(t *testing.T) {
			dir := binDir(t)
			writeFakeTreehouse(t, dir, filepath.Join(t.TempDir(), "unused-worktree"))
			home := newHome(t)
			cloneLog := replaceManagedGit(t, home, input, input, "git: 'remote-https' is not a git command. See 'git --help'.")

			added := runHand(t, home, "project", "add", input, "--mode", "direct-pr", "--name", "retry")
			if added.code == 0 || !strings.Contains(added.stderr, "git-remote-https") {
				t.Fatalf("project add = %+v, want existing missing-helper diagnosis", added)
			}
			if got, err := os.ReadFile(cloneLog); err != nil || string(got) != input+"\n" {
				t.Fatalf("clone attempts = %q, %v; want only original HTTPS", got, err)
			}
		})
	}
}

func replaceManagedGit(t *testing.T, home, original, effective string, failureLines ...string) string {
	t.Helper()
	lock, err := toolchain.LoadLock()
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(home, ".secondhand", "runtime", "bundles", lock.RuntimeID)
	gitPath := filepath.Join(bundle, "git", "git")
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	cloneLog := filepath.Join(t.TempDir(), "clones")
	failure := ""
	for _, line := range failureLines {
		failure += fmt.Sprintf("  echo %q >&2\n", line)
	}
	body := fmt.Sprintf("#!/bin/sh\nfor arg in \"$@\"; do\n  if [ \"${get_url:-}\" = 1 ]; then\n    printf '%%s\\n' %q\n    exit 0\n  fi\n  [ \"$arg\" = --get-url ] && get_url=1\ndone\nfor arg in \"$@\"; do\n  if [ \"${seen_clone:-}\" = 1 ]; then source=$arg; break; fi\n  [ \"$arg\" = clone ] && seen_clone=1\ndone\ndest=\"\"\nfor arg in \"$@\"; do dest=$arg; done\n[ \"${source:-}\" ] && printf '%%s\\n' \"$source\" >> %q\nif [ \"${source:-}\" = %q ]; then\n  mkdir -p \"$dest\"\n  : > \"$dest/partial\"\n%s  exit 1\nfi\nexec %q \"$@\"\n", effective, cloneLog, original, failure, git)
	if err := os.WriteFile(gitPath, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(bundle, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest toolchain.Target
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(body))
	component := manifest.Components["git"]
	for i := range component.Files {
		if component.Files[i].Path == "git" {
			component.Files[i].SHA256 = fmt.Sprintf("%x", digest)
		}
	}
	manifest.Components["git"] = component
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	currentPath := filepath.Join(home, ".secondhand", "runtime", "current.json")
	data, err = os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	var current toolchain.Current
	if err := json.Unmarshal(data, &current); err != nil {
		t.Fatal(err)
	}
	manifestDigest := sha256.Sum256(mustMarshal(t, manifest))
	current.ManifestSHA256 = fmt.Sprintf("%x", manifestDigest)
	data, err = json.MarshalIndent(current, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(currentPath, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return cloneLog
}

func mustMarshal(t *testing.T, target toolchain.Target) []byte {
	t.Helper()
	data, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestProjectAddNormalizesRecognizedHTTPSWhenRuntimeLacksHelper(t *testing.T) {
	for _, test := range []struct {
		name   string
		input  string
		origin string
	}{
		{name: "github", input: "https://github.com/owner/repo", origin: "git@github.com:owner/repo.git"},
		{name: "gitlab subgroup", input: "https://gitlab.com/group/subgroup/repo.git", origin: "git@gitlab.com:group/subgroup/repo.git"},
	} {
		t.Run(test.name, func(t *testing.T) {
			remote := filepath.Join(t.TempDir(), "remote")
			initGitRepo(t, remote)
			runGitIn(t, remote, "config", "receive.denyCurrentBranch", "updateInstead")
			redirectGitRemote(t, test.origin, remote)

			dir := binDir(t)
			writeFakeTreehouse(t, dir, filepath.Join(t.TempDir(), "unused-worktree"))
			home := newHome(t)
			replaceManagedGit(t, home, test.input, test.input, "git: 'remote-https' is not a git command. See 'git --help'.")

			added := runHand(t, home, "project", "add", test.input, "--mode", "direct-pr")
			if added.code != 0 {
				t.Fatalf("project add: exit %d, stderr %q", added.code, added.stderr)
			}
			if !strings.Contains(added.stdout, "Normalized HTTPS locator") || !strings.Contains(added.stdout, "no HTTPS transport helper") {
				t.Fatalf("project add stdout = %q, want visible HTTPS normalization explanation", added.stdout)
			}

			clonePath := filepath.Join(home, "projects", "repo")
			projects, err := project.List(home)
			if err != nil {
				t.Fatal(err)
			}
			if len(projects) != 1 || projects[0].URL != test.origin {
				t.Fatalf("project.List = %+v, want URL %q", projects, test.origin)
			}
			if got := runGitIn(t, clonePath, "config", "--get", "remote.origin.url"); got != test.origin+"\n" {
				t.Fatalf("clone origin = %q, want %q", got, test.origin)
			}

			runRuntimeGitIn(t, home, clonePath, "fetch", "origin")
			if err := os.WriteFile(filepath.Join(clonePath, "pushed.txt"), []byte("pushed"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGitIn(t, clonePath, "add", "pushed.txt")
			runGitIn(t, clonePath, "commit", "-q", "-m", "push through stored ssh origin")
			runRuntimeGitIn(t, home, clonePath, "push", "origin", "main")
			if _, err := os.Stat(filepath.Join(remote, "pushed.txt")); err != nil {
				t.Fatalf("push through stored SSH origin did not update remote: %v", err)
			}
		})
	}
}

func TestProjectAddPreservesHTTPSWhenRuntimeSupportsIt(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote")
	initGitRepo(t, remote)
	input := "https://github.com/owner/repo.git"
	redirectGitRemote(t, input, remote)

	dir := binDir(t)
	writeFakeTreehouse(t, dir, filepath.Join(t.TempDir(), "unused-worktree"))
	home := newHome(t)
	lock, err := toolchain.LoadLock()
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(home, ".secondhand", "runtime", "bundles", lock.RuntimeID, "git", "git-remote-https")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	added := runHand(t, home, "project", "add", input, "--mode", "direct-pr")
	if added.code != 0 {
		t.Fatalf("project add: exit %d, stderr %q", added.code, added.stderr)
	}
	if strings.Contains(added.stdout, "Normalized HTTPS locator") {
		t.Fatalf("project add stdout = %q, want original HTTPS locator preserved", added.stdout)
	}
	projects, err := project.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].URL != input {
		t.Fatalf("project.List = %+v, want URL %q", projects, input)
	}
	if got := runGitIn(t, filepath.Join(home, "projects", "repo"), "config", "--get", "remote.origin.url"); got != input+"\n" {
		t.Fatalf("clone origin = %q, want %q", got, input)
	}
}

func runRuntimeGitIn(t *testing.T, home, dir string, args ...string) {
	t.Helper()
	lock, err := toolchain.LoadLock()
	if err != nil {
		t.Fatal(err)
	}
	gitBin := filepath.Join(home, ".secondhand", "runtime", "bundles", lock.RuntimeID, "git", "git")
	spec, err := (toolchain.Runtime{GitBin: filepath.Dir(gitBin)}).Process(gitBin, args...)
	if err != nil {
		t.Fatal(err)
	}
	spec.Dir = dir
	var stdout, stderr bytes.Buffer
	spec.Stdout = &stdout
	spec.Stderr = &stderr
	if err := spec.Run(context.Background()); err != nil {
		t.Fatalf("managed git %v failed: %v: %s", args, err, stderr.String())
	}
}

// Drives add -> set-url -> list -> sync (fast-forward) -> remove through the built binary against a real local
// git remote (redirected via git's insteadOf mechanism, never the network), plus the missing-clone failure
// path not already covered by TestExitCodeThreeOnPreconditionFailure.
func TestProjectLifecycle(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote")
	initGitRepo(t, remote)
	redirectGitRemote(t, "https://example.com/demo.git", remote)
	newURL := "https://example.com/renamed-demo.git"
	redirectGitRemote(t, newURL, remote)

	dir := binDir(t)
	writeFakeTreehouse(t, dir, filepath.Join(t.TempDir(), "unused-worktree"))

	home := newHome(t)

	added := runHand(t, home, "project", "add", "https://example.com/demo.git", "--mode", "direct-pr")
	if added.code != 0 {
		t.Fatalf("project add: exit %d, stderr %q", added.code, added.stderr)
	}

	clonePath := filepath.Join(home, "projects", "demo")
	if got := runGitIn(t, clonePath, "log", "-1", "--format=%s"); got != "initial commit\n" {
		t.Fatalf("clone HEAD commit message = %q, want the remote's initial commit", got)
	}

	projects, err := project.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Name != "demo" || projects[0].URL != "https://example.com/demo.git" || projects[0].Mode != "direct-pr" {
		t.Fatalf("project.List = %+v, want a single demo/direct-pr entry", projects)
	}

	listed := runHand(t, home, "project", "list")
	if listed.code != 0 {
		t.Fatalf("project list: exit %d, stderr %q", listed.code, listed.stderr)
	}
	if !strings.Contains(listed.stdout, "demo") {
		t.Fatalf("project list stdout = %q, want it to mention demo", listed.stdout)
	}

	repointed := runHand(t, home, "project", "set-url", "demo", newURL)
	if repointed.code != 0 {
		t.Fatalf("project set-url: exit %d, stderr %q", repointed.code, repointed.stderr)
	}
	if !strings.Contains(repointed.stdout, "result: url-set") || !strings.Contains(repointed.stdout, "old_origin") || !strings.Contains(repointed.stdout, "origin") {
		t.Fatalf("project set-url stdout = %q, want both origin surfaces", repointed.stdout)
	}
	projects, err = project.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Name != "demo" || projects[0].URL != newURL || projects[0].Mode != "direct-pr" {
		t.Fatalf("project.List after set-url = %+v, want stable demo with new URL and mode", projects)
	}
	if got := runGitIn(t, clonePath, "config", "--get", "remote.origin.url"); got != newURL+"\n" {
		t.Fatalf("clone origin = %q, want %q", got, newURL)
	}

	runGitIn(t, remote, "commit", "--allow-empty", "-q", "-m", "new remote commit")

	synced := runHand(t, home, "project", "sync", "demo")
	if synced.code != 0 {
		t.Fatalf("project sync: exit %d, stderr %q", synced.code, synced.stderr)
	}
	if !strings.Contains(synced.stdout, "fast-forwarded") {
		t.Fatalf("project sync stdout = %q, want it to report a fast-forward", synced.stdout)
	}
	if got := runGitIn(t, clonePath, "log", "-1", "--format=%s"); got != "new remote commit\n" {
		t.Fatalf("clone HEAD after sync = %q, want the new remote commit fast-forwarded in", got)
	}

	badSync := runHand(t, home, "project", "sync", "nosuch")
	assertInvocation(t, badSync, 3, `project "nosuch" not registered`)

	removed := runHand(t, home, "project", "remove", "demo")
	if removed.code != 0 {
		t.Fatalf("project remove: exit %d, stderr %q", removed.code, removed.stderr)
	}
	if !strings.Contains(removed.stdout, "result: removed\n") || !strings.Contains(removed.stdout, "clone is retained") {
		t.Fatalf("project remove stdout = %q, want it to note the clone is retained", removed.stdout)
	}

	remaining, err := project.List(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("project.List after remove = %+v, want none registered", remaining)
	}
	if _, err := os.Stat(clonePath); err != nil {
		t.Fatalf("clone at %s should survive project remove: %v", clonePath, err)
	}
}

func TestLocalProjectLifecycle(t *testing.T) {
	source := filepath.Join(t.TempDir(), "book with space")
	initGitRepo(t, source)

	dir := binDir(t)
	writeFakeTreehouse(t, dir, filepath.Join(t.TempDir(), "unused-worktree"))
	home := newHome(t)

	added := runHand(t, home, "project", "add", source, "--name", "book")
	if added.code != 0 {
		t.Fatalf("local project add: exit %d, stderr %q", added.code, added.stderr)
	}
	if !strings.Contains(added.stdout, "mode: local-only") || !strings.Contains(added.stdout, "source:") {
		t.Fatalf("local project add stdout = %q, want adoption fields", added.stdout)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}

	synced := runHand(t, home, "project", "sync", "book")
	if synced.code != 0 || !strings.Contains(synced.stdout, "local-managed project") {
		t.Fatalf("local project sync = %+v, want visible local skip", synced)
	}
	repointed := runHand(t, home, "project", "set-url", "book", "https://example.com/book.git")
	if repointed.code == 0 || !strings.Contains(repointed.stderr, "local-managed project") {
		t.Fatalf("local project set-url = %+v, want refusal", repointed)
	}

	created := runHand(t, home, "project", "create", "blank")
	if created.code != 0 || !strings.Contains(created.stdout, "baseline:") || !strings.Contains(created.stdout, "mode: local-only") {
		t.Fatalf("project create = %+v, want baseline and local-only output", created)
	}
	if got := runGitIn(t, filepath.Join(home, "projects", "blank"), "log", "-1", "--format=%s"); got != "chore: initialize project\n" {
		t.Fatalf("created baseline = %q", got)
	}
}
