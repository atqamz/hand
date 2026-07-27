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
// treehouse writes banners to stderr ahead of the JSON, so the payload must be read
// from stdout alone; CombinedOutput here corrupts every parse (issue #21).
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
		return "", fmt.Errorf("treehouse get failed: %w: %s", err, strings.TrimSpace(stderr.String()))
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

// Return releases a worktree back to its treehouse pool. Returning a worktree
// that is already back in the pool is a no-op success, so teardown - which
// returns the worktree before it removes the task's state - stays retryable after
// a fault in a later step. That idempotency is treehouse's own, not something
// checked here: a returned worktree keeps its pool slot directory (the path still
// exists, the slot just flips to available), so no path-existence test can tell a
// returned worktree from a leased one. A path treehouse does not manage at all is
// still an error.
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
