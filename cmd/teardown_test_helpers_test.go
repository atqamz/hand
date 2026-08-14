package cmd

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/runtime"
)

func branchIsMerged(clonePath, worktreePath string) (bool, error) {
	branch, err := currentBranch(worktreePath)
	if err != nil {
		return false, err
	}
	base, err := defaultBranch(clonePath)
	if err != nil {
		return false, err
	}
	cmd := exec.Command("git", "branch", "--merged", base)
	cmd.Dir = clonePath
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git branch --merged %s failed: %w", base, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "*+"))
		if line == branch {
			return true, nil
		}
	}
	return false, nil
}

func closeTaskTab(client *herdr.Client, workspaceID, tabID string) error {
	return runtime.CloseTaskTab(client, workspaceID, tabID)
}
