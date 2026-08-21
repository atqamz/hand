package cmd

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/git"
	"github.com/atqamz/hand/internal/project"
)

type projectSource struct {
	input         string
	remote        bool
	root          string
	locator       string
	defaultBranch string
	baseline      string
}

func isRemoteProjectSource(source string) bool {
	for _, prefix := range []string{"https://", "git@", "ssh://", "git://"} {
		if strings.HasPrefix(source, prefix) {
			return true
		}
	}
	return false
}

func classifyProjectSource(source string) (projectSource, error) {
	if strings.TrimSpace(source) == "" {
		return projectSource{}, fmt.Errorf("project source is empty")
	}
	if isRemoteProjectSource(source) {
		return projectSource{input: source, remote: true}, nil
	}
	if project.IsFileLocator(source) {
		return projectSource{input: source}, nil
	}
	if isWindowsPath(source) {
		return projectSource{input: source}, nil
	}
	u, err := url.Parse(source)
	if err != nil {
		return projectSource{}, fmt.Errorf("invalid project source %q: %w", source, err)
	}
	if u.Scheme != "" {
		return projectSource{}, fmt.Errorf("unsupported project source scheme %q", u.Scheme)
	}
	return projectSource{input: source}, nil
}

func resolveLocalProjectSource(source projectSource) (projectSource, error) {
	path := source.input
	if project.IsFileLocator(path) {
		var err error
		path, err = project.FileLocatorPath(path)
		if err != nil {
			return projectSource{}, err
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return projectSource{}, fmt.Errorf("local project source %q: %w", source.input, err)
	}
	if !info.IsDir() {
		return projectSource{}, fmt.Errorf("local project source %q is not a directory", source.input)
	}
	root, err := git.ResolveRoot(path)
	if err != nil {
		return projectSource{}, fmt.Errorf("local project source %q is not a Git worktree: %w", source.input, err)
	}
	bare, err := git.IsBare(root)
	if err != nil {
		return projectSource{}, fmt.Errorf("inspect local project source %q: %w", source.input, err)
	}
	if bare {
		return projectSource{}, fmt.Errorf("local project source %q is a bare Git repository; Hand requires a non-bare worktree", source.input)
	}
	if _, err := git.HeadCommit(root); err != nil {
		return projectSource{}, fmt.Errorf("local project source %q has no committed HEAD; commit a baseline or use `hand project create <name>`: %w", source.input, err)
	}
	if _, err := git.CurrentBranch(root); err != nil {
		return projectSource{}, fmt.Errorf("local project source %q must have a checked-out branch: %w", source.input, err)
	}
	defaultBranch, err := git.LocalDefaultBranch(root)
	if err != nil {
		return projectSource{}, fmt.Errorf("local project source %q has no stable default branch: %w", source.input, err)
	}
	baseline, err := git.BranchCommit(root, defaultBranch)
	if err != nil {
		return projectSource{}, fmt.Errorf("local project source %q default branch %q is not committed: %w", source.input, defaultBranch, err)
	}
	dirty, err := git.HasUncommittedChanges(root)
	if err != nil {
		return projectSource{}, fmt.Errorf("inspect local project source %q: %w", source.input, err)
	}
	if dirty {
		return projectSource{}, fmt.Errorf("local Git source has uncommitted or untracked changes; Hand adopts committed repository state only; commit, stash, or remove the changes before adding this Project")
	}
	locator, err := project.CanonicalFileLocator(root)
	if err != nil {
		return projectSource{}, fmt.Errorf("canonicalize local project source %q: %w", source.input, err)
	}
	source.root = root
	source.locator = locator
	source.defaultBranch = defaultBranch
	source.baseline = baseline
	return source, nil
}

func isWindowsPath(value string) bool {
	return len(value) >= 2 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':'
}

func gitCloneLocal(source, dest string) error {
	cmd := exec.Command("git", "clone", "--no-local", source, dest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func prepareAdoptedClone(source projectSource, dest string) error {
	if err := git.CheckoutBranch(dest, source.defaultBranch); err != nil {
		return err
	}
	if got, err := git.BranchCommit(dest, source.defaultBranch); err != nil || got != source.baseline {
		if err != nil {
			return fmt.Errorf("verify adopted default branch %q: %w", source.defaultBranch, err)
		}
		return fmt.Errorf("adopted default branch %q commit %s differs from source commit %s", source.defaultBranch, got, source.baseline)
	}
	bare, err := git.IsBare(dest)
	if err != nil {
		return err
	}
	if bare {
		return fmt.Errorf("adopted project clone %q is bare", dest)
	}
	hasAlternates, err := git.HasAlternates(dest)
	if err != nil {
		return err
	}
	if hasAlternates {
		return fmt.Errorf("adopted project clone %q uses a shared Git object store", dest)
	}
	if err := git.RemoveOrigin(dest); err != nil {
		return err
	}
	if err := git.PreserveDefaultBranch(dest, source.defaultBranch); err != nil {
		return err
	}
	return nil
}

func initCreatedProject(path string) (string, error) {
	init := exec.Command("git", "init", "--initial-branch=main", path)
	if out, err := init.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git init failed: %s", strings.TrimSpace(string(out)))
	}
	commit := exec.Command("git", "-C", path, "-c", "user.name=hand", "-c", "user.email=hand@localhost", "commit", "--allow-empty", "-m", "chore: initialize project")
	if out, err := commit.CombinedOutput(); err != nil {
		return "", fmt.Errorf("create project baseline commit failed: %s", strings.TrimSpace(string(out)))
	}
	baseline, err := git.HeadCommit(path)
	if err != nil {
		return "", err
	}
	return baseline, nil
}

func projectNameFromRoot(root string) string {
	return filepath.Base(filepath.Clean(root))
}
