//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/project"
)

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
