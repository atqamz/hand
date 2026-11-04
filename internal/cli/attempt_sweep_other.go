//go:build !linux

package cli

func sweepWorktree(string) {}

func worktreeBusy(string) bool { return false }
