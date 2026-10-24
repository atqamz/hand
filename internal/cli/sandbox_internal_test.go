package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLocalRemoteNeedsARepository(t *testing.T) {
	root := realPath(t.TempDir())
	t.Setenv("HOME", filepath.Join(root, "home"))
	worktree := filepath.Join(root, "work")
	mkdirs(t, worktree)
	repo := filepath.Join(root, "repo")
	bare := filepath.Join(root, "bare.git")
	mkdirs(t, filepath.Join(repo, ".git"), worktree+"/relative", filepath.Join(root, "plain"))
	mkdirs(t, bare, filepath.Join(bare, "objects"))
	if err := os.WriteFile(filepath.Join(bare, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		url  string
		want string
	}{
		{repo, filepath.Join(repo, ".git")},
		{bare, bare},
		{"file://" + repo, filepath.Join(repo, ".git")},
		{"../repo", filepath.Join(repo, ".git")},
	} {
		if got, ok := localRemote(c.url, worktree, func() []string { return nil }); !ok || got != realPath(c.want) {
			t.Fatalf("localRemote(%q) = %q, %v", c.url, got, ok)
		}
	}
	for _, url := range []string{
		filepath.Join(root, "plain"),
		"https://example.com/repo.git",
		"git@example.com:repo.git",
		filepath.Join(root, "missing"),
	} {
		if got, ok := localRemote(url, worktree, func() []string { return nil }); ok {
			t.Fatalf("localRemote(%q) accepted %q", url, got)
		}
	}
}

func TestLocalRemoteUnderHomeNeedsARegisteredProject(t *testing.T) {
	home := realPath(t.TempDir())
	t.Setenv("HOME", home)
	worktree := filepath.Join(home, "work")
	repo := filepath.Join(home, "src", "repo")
	mkdirs(t, worktree, filepath.Join(repo, ".git"))

	if got, ok := localRemote(repo, worktree, func() []string { return nil }); ok {
		t.Fatalf("an unregistered remote under HOME was accepted: %q", got)
	}
	if got, ok := localRemote(repo, worktree, func() []string { return []string{repo} }); !ok || got != realPath(filepath.Join(repo, ".git")) {
		t.Fatalf("a registered remote under HOME = %q, %v", got, ok)
	}
	if got, ok := localRemote(repo, worktree, func() []string { return []string{filepath.Join(home, "src", "other")} }); ok {
		t.Fatalf("a different registered project accepted %q", got)
	}
}

func TestLinkedAdminDirFindsOnlyItsWorktree(t *testing.T) {
	root := realPath(t.TempDir())
	common := filepath.Join(root, "repo", ".git")
	worktree := filepath.Join(root, "t1-a2")
	admin := filepath.Join(common, "worktrees", "t1-a2")
	other := filepath.Join(common, "worktrees", "t1-a3")
	mkdirs(t, admin, other, worktree)
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+admin+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(admin, "gitdir"), []byte(filepath.Join(worktree, ".git")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "gitdir"), []byte(filepath.Join(root, "t1-a3", ".git")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := linkedAdminDir(common, worktree); got != admin {
		t.Fatalf("linked admin dir = %q, want %q", got, admin)
	}
	if got := linkedAdminDir(common, filepath.Join(root, "missing")); got != "" {
		t.Fatalf("an unrelated worktree got %q", got)
	}
	if got := linkedAdminDir("", worktree); got != "" {
		t.Fatalf("empty common dir got %q", got)
	}
}

func TestGitDirAtNeedsARealGitFolder(t *testing.T) {
	root := realPath(t.TempDir())
	repo := filepath.Join(root, "repo")
	bare := filepath.Join(root, "bare.git")
	plain := filepath.Join(root, "plain")
	mkdirs(t, filepath.Join(repo, ".git"), bare, filepath.Join(bare, "objects"), plain)
	if err := os.WriteFile(filepath.Join(bare, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := gitDirAt(repo); got != filepath.Join(repo, ".git") {
		t.Fatalf("git dir = %q", got)
	}
	if got := gitDirAt(bare); got != bare {
		t.Fatalf("bare git dir = %q", got)
	}
	if got := gitDirAt(plain); got != "" {
		t.Fatalf("plain folder got %q", got)
	}

	link := filepath.Join(root, "linked")
	mkdirs(t, link)
	if err := os.Symlink(filepath.Join(repo, ".git"), filepath.Join(link, ".git")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if got := gitDirAt(link); got != "" {
		t.Fatalf("a symlinked git dir got %q", got)
	}
}
