// Package worktree wraps treehouse worktree acquisition and the spawn collision guard.
package worktree

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/atqamz/hand/internal/state"
)

// Known non-force aborts are classified while the same lease remains held.
var ErrReturnAborted = errors.New("treehouse return aborted while lease remains owned")

type returnAbortedError struct {
	message string
}

func (e *returnAbortedError) Error() string { return e.message }

func (e *returnAbortedError) Unwrap() error { return ErrReturnAborted }

// Lease is one acquisition of a treehouse pool slot. ID is empty when treehouse
// reported no identity, which a version older than v2.1.0 does.
type Lease struct {
	Path string
	ID   string
}

type statusEntry struct {
	Path    string `json:"path"`
	Status  string `json:"status"`
	LeaseID string `json:"lease_id"`
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
	// CombinedOutput here corrupts every parse (atqamz/hand#21).
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
	// An attempt that never reached a lease has no slot to release.
	if worktreePath == "" {
		return nil
	}
	args := []string{"return", worktreePath}
	if force {
		args = append(args, "--force")
	}
	out, err := exec.Command("treehouse", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("treehouse return failed: %s", strings.TrimSpace(string(out)))
	}
	if !force && strings.Contains(string(out), "Aborted") {
		return &returnAbortedError{message: fmt.Sprintf("treehouse return aborted, worktree %s is still leased: %s", worktreePath, strings.TrimSpace(string(out)))}
	}
	return nil
}

func VerifyLease(worktreePath, expectedLeaseID string) error {
	if worktreePath == "" || expectedLeaseID == "" {
		return fmt.Errorf("cannot verify treehouse lease without path and lease ID")
	}
	cmd := exec.Command("treehouse", "status", "--json")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("treehouse status failed: %w", err)
	}
	var entries []statusEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return fmt.Errorf("parse treehouse status output: %w", err)
	}
	for _, entry := range entries {
		if entry.Path == worktreePath && entry.Status == "leased" && entry.LeaseID == expectedLeaseID {
			return nil
		}
	}
	return fmt.Errorf("treehouse lease for %s does not match expected lease %s", worktreePath, expectedLeaseID)
}

// CheckCollision cross-checks a freshly acquired lease against every other task's recorded one,
// returning the ID of the conflicting task or "" for no collision.
func CheckCollision(homeDir string, lease Lease, excludeID string) (string, error) {
	histories, err := state.ListOpenHistories(homeDir)
	if err != nil {
		return "", err
	}
	for _, history := range histories {
		if history.Task.ID == excludeID || history.ActiveAttempt == nil {
			continue
		}
		attempt := history.ActiveAttempt
		if lease.ID != "" && attempt.LeaseID != "" {
			// Identity rather than path: a pool slot path is recycled across leases while an
			// identity never is, so a row a failed teardown left behind still names a path
			// treehouse has freed, and path equality refused the next spawn over that.
			if attempt.LeaseID == lease.ID {
				return history.Task.ID, nil
			}
			continue
		}
		// Fallback whenever either side has no identity, which is any row written before the
		// lease_id column existed.
		if attempt.Worktree == lease.Path {
			return history.Task.ID, nil
		}
	}
	return "", nil
}
