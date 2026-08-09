//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/project"
)

// Drives add -> list -> sync (fast-forward) -> remove through the built binary against a real local git
// remote (redirected via git's insteadOf mechanism, never the network), plus the one failure path not
// already covered by TestExitCodeThreeOnPreconditionFailure: sync against a project never cloned to disk.
func TestProjectLifecycle(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote")
	initGitRepo(t, remote)
	redirectGitRemote(t, "https://example.com/demo.git", remote)

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
