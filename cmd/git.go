package cmd

import (
	"github.com/atqamz/hand/internal/git"
)

func hasUncommittedChanges(worktreePath string) (bool, error) {
	return git.HasUncommittedChanges(worktreePath)
}

// git symbolic-ref, not rev-parse --abbrev-ref: the latter exits 0 and prints the literal string
// "HEAD" on a detached HEAD instead of failing, which every caller would otherwise have to
// special-case itself to avoid treating that sentinel as a real branch name.
func currentBranch(worktreePath string) (string, error) {
	return git.CurrentBranch(worktreePath)
}

func defaultBranch(clonePath string) (string, error) {
	return git.DefaultBranch(clonePath)
}
