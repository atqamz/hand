//go:build contract

package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A pool of its own under the test's temp directory, so the operator's real
// pool is neither read nor written: treehouse keeps all of its state, leases
// included, under the root the config names.
func newPool(t *testing.T, maxTrees int) string {
	t.Helper()
	dir := newRepo(t)
	config := fmt.Sprintf("max_trees = %d\nroot = \"./\"\n", maxTrees)
	if err := os.WriteFile(filepath.Join(dir, "treehouse.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A scratch git config for the test's own commits and for the ones treehouse
// makes on its behalf, so the operator's commit.gpgsign never drags gpg-agent
// into a scratch pool.
func newRepo(t *testing.T) string {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	content := "[user]\n\tname = hand-contract\n\temail = hand-contract@example.invalid\n" +
		"[commit]\n\tgpgsign = false\n[tag]\n\tgpgsign = false\n[init]\n\tdefaultBranch = main\n"
	if err := os.WriteFile(cfg, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	dir := t.TempDir()
	run(t, dir, "git", "init", "-q", "-b", "main", ".").requireCode(t, 0)
	run(t, dir, "git", "commit", "-q", "--allow-empty", "-m", "init").requireCode(t, 0)
	return dir
}

type lease struct {
	Path    string `json:"path"`
	LeaseID string `json:"lease_id"`
}

type statusEntry struct {
	Path    string `json:"path"`
	Status  string `json:"status"`
	LeaseID string `json:"lease_id"`
}

func acquire(t *testing.T, dir, holder string) lease {
	t.Helper()
	res := run(t, dir, "treehouse", "get", "--lease", "--json", "--lease-holder", holder).requireCode(t, 0)
	if res.stderr == "" {
		t.Fatal("stderr is empty, so nothing proves the banner does not reach stdout")
	}
	var got lease
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil {
		t.Fatalf("parse stdout %q: %v", res.stdout, err)
	}
	if got.Path == "" || got.LeaseID == "" {
		t.Fatalf("lease = %+v, want a path and a lease identity", got)
	}
	return got
}

func TestTreehouseLeaseIsExclusiveUntilReturned(t *testing.T) {
	requireBin(t, "treehouse")
	dir := newPool(t, 1)

	first := acquire(t, dir, "hand:contract-1")
	if _, err := os.Stat(first.Path); err != nil {
		t.Fatalf("leased worktree does not exist: %v", err)
	}

	run(t, dir, "treehouse", "get", "--lease", "--json").
		requireCode(t, 1).
		requireStderrContains(t, "in use or dirty (max_trees = 1)")

	run(t, dir, "treehouse", "return", first.Path).
		requireCode(t, 0).
		requireStderrContains(t, "Worktree returned to pool.")
	if _, err := os.Stat(first.Path); err != nil {
		t.Fatalf("returned worktree directory is gone, so teardown cannot tell a returned slot from a leased one: %v", err)
	}

	run(t, dir, "treehouse", "return", first.Path).requireCode(t, 0)

	second := acquire(t, dir, "hand:contract-2")
	if second.Path != first.Path {
		t.Fatalf("second lease path %q, want the freed slot %q", second.Path, first.Path)
	}
	if second.LeaseID == first.LeaseID {
		t.Fatalf("lease identity %q reused across acquisitions, so CheckCollision cannot tell them apart", second.LeaseID)
	}
	run(t, dir, "treehouse", "return", second.Path).requireCode(t, 0)
}

func TestTreehouseGetCanAdvanceAWorktreeToTheRemoteDefaultBranch(t *testing.T) {
	requireBin(t, "treehouse")
	pool := newPool(t, 1)
	remote := filepath.Join(t.TempDir(), "remote.git")
	if err := os.MkdirAll(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, remote, "git", "init", "--bare", "-q").requireCode(t, 0)
	run(t, pool, "git", "remote", "add", "origin", remote).requireCode(t, 0)
	initial := run(t, pool, "git", "rev-parse", "HEAD").requireCode(t, 0).stdout
	run(t, pool, "git", "push", "-q", "-u", "origin", "main").requireCode(t, 0)
	run(t, pool, "git", "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main").requireCode(t, 0)
	run(t, pool, "git", "commit", "--allow-empty", "-q", "-m", "remote advance").requireCode(t, 0)
	remoteHead := run(t, pool, "git", "rev-parse", "HEAD").requireCode(t, 0).stdout
	run(t, pool, "git", "push", "-q", "origin", "main").requireCode(t, 0)
	run(t, pool, "git", "reset", "--hard", "-q", strings.TrimSpace(initial)).requireCode(t, 0)

	lease := acquire(t, pool, "hand:contract-remote-advance")
	got := strings.TrimSpace(run(t, lease.Path, "git", "rev-parse", "HEAD").requireCode(t, 0).stdout)
	if got != strings.TrimSpace(remoteHead) {
		t.Fatalf("acquired worktree HEAD = %q, want remote default branch tip %q", got, strings.TrimSpace(remoteHead))
	}
	local := strings.TrimSpace(run(t, pool, "git", "rev-parse", "refs/heads/main").requireCode(t, 0).stdout)
	if local != strings.TrimSpace(initial) {
		t.Fatalf("registered local default branch = %q, want planned revision %q", local, strings.TrimSpace(initial))
	}
	run(t, pool, "treehouse", "return", "--force", lease.Path).requireCode(t, 0)
}

func TestTreehouseStatusReportsTheCurrentLeaseIdentity(t *testing.T) {
	requireBin(t, "treehouse")
	dir := newPool(t, 1)
	lease := acquire(t, dir, "hand:contract-status")

	status := run(t, dir, "treehouse", "status", "--json").requireCode(t, 0).stdout
	var entries []statusEntry
	if err := json.Unmarshal([]byte(status), &entries); err != nil {
		t.Fatalf("parse status %q: %v", status, err)
	}
	if len(entries) != 1 || entries[0].Path != lease.Path || entries[0].Status != "leased" || entries[0].LeaseID != lease.LeaseID {
		t.Fatalf("status = %+v, want current lease %s at %s", entries, lease.LeaseID, lease.Path)
	}
	run(t, dir, "treehouse", "return", lease.Path).requireCode(t, 0)
}

func TestTreehouseRefusesAnUnmanagedPath(t *testing.T) {
	requireBin(t, "treehouse")
	dir := newPool(t, 1)
	outside := filepath.Join(t.TempDir(), "elsewhere")

	run(t, dir, "treehouse", "return", outside).
		requireCode(t, 1).
		requireStderrContains(t, "is not managed by treehouse")
}

// The one failure the exit status does not report, which is why worktree.Return
// reads the output instead of trusting the code.
func TestTreehouseAbortsAnUnforcedDirtyReturnAtExitZero(t *testing.T) {
	requireBin(t, "treehouse")
	dir := newPool(t, 1)
	leased := acquire(t, dir, "hand:contract-dirty")

	if err := os.WriteFile(filepath.Join(leased.Path, "uncommitted.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run(t, dir, "treehouse", "return", leased.Path).
		requireCode(t, 0).
		requireStderrContains(t, "Aborted")

	run(t, dir, "treehouse", "get", "--lease", "--json").
		requireCode(t, 1).
		requireStderrContains(t, "in use or dirty")

	run(t, dir, "treehouse", "return", leased.Path, "--force").requireCode(t, 0)
	if _, err := os.Stat(filepath.Join(leased.Path, "uncommitted.txt")); !os.IsNotExist(err) {
		t.Fatalf("forced return left the uncommitted file behind: %v", err)
	}
	forced := acquire(t, dir, "hand:contract-after-force")
	run(t, dir, "treehouse", "return", forced.Path).requireCode(t, 0)
}

// The behaviour cmd/project.go's excludeLocally exists for: hand has to exclude
// the config treehouse leaves untracked, or the clone reads dirty forever.
func TestTreehouseInitLeavesTheConfigUntracked(t *testing.T) {
	requireBin(t, "treehouse")
	dir := newRepo(t)

	run(t, dir, "treehouse", "init").requireCode(t, 0)

	status := run(t, dir, "git", "status", "--porcelain").requireCode(t, 0).stdout
	if !strings.Contains(status, "?? treehouse.toml") {
		t.Fatalf("git status = %q, want treehouse.toml reported untracked", status)
	}
	for _, name := range []string{".gitignore", filepath.Join(".git", "info", "exclude")} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "treehouse.toml") {
			t.Fatalf("%s already excludes treehouse.toml, so hand need not", name)
		}
	}

	run(t, dir, "treehouse", "init").
		requireCode(t, 1).
		requireStderrContains(t, "treehouse.toml already exists")
}
