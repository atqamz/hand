package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/cli"
)

func TestGitCreatesAWorktreeBeyond260Characters(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	ctx := context.Background()
	repo := t.TempDir()
	deep := filepath.Join(strings.Repeat("d", 120), strings.Repeat("e", 120), "file.txt")
	if err := os.MkdirAll(filepath.Join(repo, filepath.Dir(deep)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, deep), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "x"},
	} {
		if _, err := cli.Git(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	wt := filepath.Join(t.TempDir(), strings.Repeat("w", 100), "t1-a1")
	if len(filepath.Join(wt, deep)) <= 260 {
		t.Fatal("test path is too short")
	}
	if _, err := cli.Git(ctx, repo, "worktree", "add", "-q", "-b", "b", wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wt, deep)); err != nil {
		t.Fatal(err)
	}
}
