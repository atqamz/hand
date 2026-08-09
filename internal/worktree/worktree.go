// Package worktree wraps treehouse worktree acquisition and the spawn collision guard.
package worktree

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/atqamz/hand/internal/state"
)

// Lease is one acquisition of a treehouse pool slot. ID is empty when treehouse
// reported no identity, which a version older than v2.1.0 does.
type Lease struct {
	Path string
	ID   string
}

// Get acquires a worktree from the project clone's treehouse pool. clonePath must be the project
// clone directory, because treehouse resolves the pool from its own working directory.
func Get(clonePath, leaseHolder string) (Lease, error) {
	args := []string{"get", "--lease", "--json"}
	if leaseHolder != "" {
		args = append(args, "--lease-holder", leaseHolder)
	}
	cmd := exec.Command("treehouse", args...)
	cmd.Dir = clonePath
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// Banners land on stderr ahead of the JSON, so the payload has to be read from stdout alone -
	// CombinedOutput here corrupts every parse (atqamz/secondhand#21).
	out, err := cmd.Output()
	if err != nil {
		return Lease{}, fmt.Errorf("treehouse get failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	var payload struct {
		Path    string `json:"path"`
		LeaseID string `json:"lease_id"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return Lease{}, fmt.Errorf("parse treehouse get output: %w", err)
	}
	if payload.Path == "" {
		return Lease{}, fmt.Errorf("treehouse get returned no worktree path")
	}
	return Lease{Path: payload.Path, ID: payload.LeaseID}, nil
}

// Return releases a worktree back to its treehouse pool. A repeated return is a
// no-op success; an aborted one exits 0 with the slot still leased, so the exit
// status alone cannot say a return happened. See internal/faketool/FIDELITY.md.
func Return(worktreePath string, force bool) error {
	args := []string{"return", worktreePath}
	if force {
		args = append(args, "--force")
	}
	out, err := exec.Command("treehouse", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("treehouse return failed: %s", strings.TrimSpace(string(out)))
	}
	if !force && strings.Contains(string(out), "Aborted") {
		return fmt.Errorf("treehouse return aborted, worktree %s is still leased: %s", worktreePath, strings.TrimSpace(string(out)))
	}
	return nil
}

// CheckCollision cross-checks a freshly acquired lease against every other task's recorded one,
// returning the ID of the conflicting task or "" for no collision.
func CheckCollision(homeDir string, lease Lease, excludeID string) (string, error) {
	tasks, err := state.List(homeDir)
	if err != nil {
		return "", err
	}
	// Done and failed rows are compared too, because a task holds its lease until teardown
	// returns it.
	for _, t := range tasks {
		if t.ID == excludeID {
			continue
		}
		if lease.ID != "" && t.LeaseID != "" {
			// Identity rather than path: a pool slot path is recycled across leases while an
			// identity never is, so a row a failed teardown left behind still names a path
			// treehouse has freed, and path equality refused the next spawn over that.
			if t.LeaseID == lease.ID {
				return t.ID, nil
			}
			continue
		}
		// Fallback whenever either side has no identity, which is any row written before the
		// lease_id column existed.
		if t.Worktree == lease.Path {
			return t.ID, nil
		}
	}
	return "", nil
}
