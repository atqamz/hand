// Package worktree wraps treehouse worktree acquisition and the spawn collision guard.
package worktree

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/atqamz/secondhand/internal/state"
)

// Get acquires a worktree from the project clone's treehouse pool.
// clonePath must be the project clone directory (treehouse resolves the pool from cwd).
func Get(clonePath, leaseHolder string) (string, error) {
	args := []string{"get", "--lease", "--json"}
	if leaseHolder != "" {
		args = append(args, "--lease-holder", leaseHolder)
	}
	cmd := exec.Command("treehouse", args...)
	cmd.Dir = clonePath
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("treehouse get failed: %s", strings.TrimSpace(stderr.String()))
	}

	var lease struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(out, &lease); err != nil {
		return "", fmt.Errorf("parse treehouse get output: %w", err)
	}
	if lease.Path == "" {
		return "", fmt.Errorf("treehouse get returned no worktree path")
	}
	return lease.Path, nil
}

// Return releases a worktree back to its treehouse pool.
func Return(worktreePath string, force bool) error {
	args := []string{"return", worktreePath}
	if force {
		args = append(args, "--force")
	}
	out, err := exec.Command("treehouse", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("treehouse return failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// CheckCollision cross-checks worktreePath against every other active task's recorded
// worktree path, guarding against the stale-lease-after-crash bug (firstmate #947).
// It returns the ID of the conflicting task, or "" if there is no collision.
func CheckCollision(homeDir, worktreePath, excludeID string) (string, error) {
	tasks, err := state.List(homeDir)
	if err != nil {
		return "", err
	}
	for _, t := range tasks {
		if t.ID == excludeID {
			continue
		}
		if t.Worktree == worktreePath {
			return t.ID, nil
		}
	}
	return "", nil
}
