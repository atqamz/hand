package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	t.Setenv("USERPROFILE", filepath.Join(root, "home"))
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
	t.Setenv("USERPROFILE", home)
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

func TestLinkedAdminDirAcceptsADigitSuffix(t *testing.T) {
	root := realPath(t.TempDir())
	common := filepath.Join(root, "repo", ".git")
	worktree := filepath.Join(root, "t1-a1")
	first := filepath.Join(common, "worktrees", "t1-a1")
	second := filepath.Join(common, "worktrees", "t1-a11")
	mkdirs(t, first, second, worktree)
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+second+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "gitdir"), []byte(filepath.Join(worktree, ".git")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first, "gitdir"), []byte(filepath.Join(root, "t1-a2", ".git")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := linkedAdminDir(common, worktree); got != second {
		t.Fatalf("the digit-suffixed entry = %q, want %q", got, second)
	}
}

func TestLinkedAdminDirRejectsANonDigitSuffix(t *testing.T) {
	root := realPath(t.TempDir())
	common := filepath.Join(root, "repo", ".git")
	worktree := filepath.Join(root, "t1-a1")
	entry := filepath.Join(common, "worktrees", "t1-a1x")
	mkdirs(t, entry, worktree)
	if err := os.WriteFile(filepath.Join(entry, "gitdir"), []byte(filepath.Join(worktree, ".git")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+entry+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := linkedAdminDir(common, worktree); got != "" {
		t.Fatalf("a non-digit suffix got %q, want nothing", got)
	}
}

func TestLinkedAdminDirRejectsASiblingBaseName(t *testing.T) {
	root := realPath(t.TempDir())
	common := filepath.Join(root, "repo", ".git")
	worktree := filepath.Join(root, "t1-a2")
	sibling := filepath.Join(common, "worktrees", "t1-a3")
	mkdirs(t, sibling, worktree)
	if err := os.WriteFile(filepath.Join(sibling, "gitdir"), []byte(filepath.Join(worktree, ".git")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+sibling+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := linkedAdminDir(common, worktree); got != "" {
		t.Fatalf("a sibling base name got %q, want nothing", got)
	}
}

func TestLinkedAdminDirIgnoresASymlinkedDotGit(t *testing.T) {
	root := realPath(t.TempDir())
	common := filepath.Join(root, "repo", ".git")
	worktree := filepath.Join(root, "t1-a2")
	sibling := filepath.Join(root, "t1-a3")
	admin := filepath.Join(common, "worktrees", "t1-a3")
	mkdirs(t, admin, worktree, sibling)
	if err := os.WriteFile(filepath.Join(admin, "gitdir"), []byte(filepath.Join(sibling, ".git")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, ".git"), []byte("gitdir: "+admin+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(sibling, ".git"), filepath.Join(worktree, ".git")); err != nil {
		t.Fatal(err)
	}
	if got := linkedAdminDir(common, worktree); got != "" {
		t.Fatalf("a symlinked .git got %q, want nothing", got)
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

func TestGitRemotesComeFromTheTrustedCommonDir(t *testing.T) {
	root := realPath(t.TempDir())
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", filepath.Join(root, "home"))
	wt := filepath.Join(root, "wt")
	common := filepath.Join(root, "repo", ".git")
	remote := filepath.Join(root, "remote.git")
	fake := filepath.Join(root, "fake")
	mkdirs(t, wt, common, remote, filepath.Join(remote, "objects"), fake)
	if err := os.WriteFile(filepath.Join(remote, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(common, "config"), []byte("[remote \"origin\"]\n\turl = "+strings.ReplaceAll(remote, `\`, `\\`)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fake, "config"), []byte("[remote \"evil\"]\n\turl = "+strings.ReplaceAll(fake, `\`, `\\`)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+fake+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := gitRemotes(context.Background(), wt, common, func() []string { return nil })
	if len(got) != 1 || got[0] != realPath(remote) {
		t.Fatalf("remotes = %v, want only %q", got, realPath(remote))
	}
	if got := gitRemotes(context.Background(), wt, "", func() []string { return nil }); got != nil {
		t.Fatalf("an empty common dir read remotes: %v", got)
	}
	if got := gitRemotes(context.Background(), wt, filepath.Join(wt, ".git"), func() []string { return nil }); got != nil {
		t.Fatalf("a common dir inside the worktree read remotes: %v", got)
	}
}
