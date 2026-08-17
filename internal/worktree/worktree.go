// Package worktree wraps treehouse worktree acquisition and the spawn collision guard.
package worktree

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

type LeaseObservationState string

const (
	LeaseExact      LeaseObservationState = "exact"
	LeaseAbsent     LeaseObservationState = "absent"
	LeaseMismatch   LeaseObservationState = "mismatch"
	LeaseUnprovable LeaseObservationState = "unprovable"
	LeaseUnknown    LeaseObservationState = "unknown"
)

// LeaseProbe records how an unobservable pool was probed. WorkingDir is load-bearing evidence:
// treehouse resolves the pool from it, so a pool key that moved reports nothing from a worktree
// whose lease is still held in the pool it was acquired from.
type LeaseProbe struct {
	Command    string
	WorkingDir string
	Reason     string
}

type LeaseObservation struct {
	State   LeaseObservationState
	LeaseID string
	Probe   LeaseProbe
}

const statusCommand = "treehouse status --json"

// UnprovenLeaseError refuses an action that requires proven ownership. Observation carries the
// classification, so a caller can tell an unobservable pool from a lease that belongs elsewhere.
type UnprovenLeaseError struct {
	WorktreePath    string
	ExpectedLeaseID string
	Observation     LeaseObservation
}

func (e *UnprovenLeaseError) Error() string {
	expected := e.ExpectedLeaseID
	if expected == "" {
		expected = "(none recorded)"
	}
	switch e.Observation.State {
	case LeaseUnknown:
		return fmt.Sprintf(
			"treehouse lease for %s could not be observed: %s; observed by running %q with working directory %s, which is what selects the pool; expected lease %s; destructive cleanup refused because ownership could not be proven, not because a lease mismatched",
			e.WorktreePath, e.Observation.Probe.Reason, e.Observation.Probe.Command, e.Observation.Probe.WorkingDir, expected)
	case LeaseMismatch:
		return fmt.Sprintf("treehouse lease for %s is held as %s, not the expected %s; the worktree belongs to another owner", e.WorktreePath, e.Observation.LeaseID, expected)
	case LeaseAbsent:
		return fmt.Sprintf("treehouse reports no lease held on %s; expected lease %s", e.WorktreePath, expected)
	case LeaseUnprovable:
		return fmt.Sprintf("treehouse lease for %s has no comparable identity; expected lease %s; destructive cleanup refused because ownership could not be proven, not because a lease mismatched", e.WorktreePath, expected)
	}
	return fmt.Sprintf("treehouse lease for %s is %s, which does not prove ownership of expected lease %s", e.WorktreePath, e.Observation.State, expected)
}

type Cleanliness string

const (
	Clean Cleanliness = "clean"
	Dirty Cleanliness = "dirty"
)

// ObserveLease classifies recorded worktree ownership and never fails: every cause that stops the
// pool from being observed is reported as LeaseUnknown carrying its probe, because an observation
// that could not be made is not evidence that ownership changed.
func ObserveLease(worktreePath, expectedLeaseID string) LeaseObservation {
	if worktreePath == "" {
		return LeaseObservation{State: LeaseAbsent}
	}
	if _, err := os.Stat(worktreePath); err != nil {
		if os.IsNotExist(err) {
			return LeaseObservation{State: LeaseAbsent}
		}
		return unknownLease(worktreePath, fmt.Sprintf("inspect worktree path: %v", err))
	}
	entries, reason := treehouseStatus(worktreePath)
	if reason != "" {
		return unknownLease(worktreePath, reason)
	}
	for _, entry := range entries {
		if entry.Path != worktreePath {
			continue
		}
		if entry.Status != "leased" {
			return LeaseObservation{State: LeaseAbsent, LeaseID: entry.LeaseID}
		}
		if expectedLeaseID == "" || entry.LeaseID == "" {
			return LeaseObservation{State: LeaseUnprovable, LeaseID: entry.LeaseID}
		}
		if entry.LeaseID == expectedLeaseID {
			return LeaseObservation{State: LeaseExact, LeaseID: entry.LeaseID}
		}
		return LeaseObservation{State: LeaseMismatch, LeaseID: entry.LeaseID}
	}
	if len(entries) == 0 {
		return unknownLease(worktreePath, "treehouse reported no pool entries")
	}
	return unknownLease(worktreePath, fmt.Sprintf("treehouse reported %d pool entries and none names this worktree; the first is %s", len(entries), entries[0].Path))
}

func unknownLease(worktreePath, reason string) LeaseObservation {
	return LeaseObservation{State: LeaseUnknown, Probe: LeaseProbe{Command: statusCommand, WorkingDir: worktreePath, Reason: reason}}
}

func ObserveCleanliness(worktreePath string) (Cleanliness, error) {
	cmd := exec.Command("git", "-C", worktreePath, "status", "--porcelain", "--untracked-files=all")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git worktree status failed: %w", err)
	}
	if len(out) != 0 {
		return Dirty, nil
	}
	return Clean, nil
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

// Reads the full commit object ID currently checked out by a leased worktree.
func HeadCommit(worktreePath string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--verify", "HEAD^{commit}")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolve worktree HEAD: %w", err)
	}
	commit := strings.TrimSpace(string(out))
	if commit == "" {
		return "", fmt.Errorf("resolve worktree HEAD: empty Git object ID")
	}
	return commit, nil
}

// Return releases a worktree back to its treehouse pool without lease identity.
func Return(worktreePath string, force bool) error {
	if worktreePath == "" {
		return nil
	}
	args := []string{"return", worktreePath}
	if force {
		args = append(args, "--force")
	}
	return returnTreehouse(worktreePath, args, force)
}

// ReturnLease releases a worktree only when treehouse still owns the expected lease.
func ReturnLease(worktreePath, leaseID string, force bool) error {
	if worktreePath == "" {
		return nil
	}
	if leaseID == "" {
		return Return(worktreePath, force)
	}
	args := []string{"return"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, "--if-lease-id", leaseID, worktreePath)
	return returnTreehouse(worktreePath, args, force)
}

func returnTreehouse(worktreePath string, args []string, force bool) error {
	out, err := exec.Command("treehouse", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("treehouse return failed: %s", strings.TrimSpace(string(out)))
	}
	if !force && strings.Contains(string(out), "Aborted") {
		return &returnAbortedError{message: fmt.Sprintf("treehouse return aborted, worktree %s is still leased: %s", worktreePath, strings.TrimSpace(string(out)))}
	}
	return nil
}

// Reports the pool entries, or the reason the pool could not be observed. A missing executable, a
// non-zero exit and unparsable output are all unobservability, never absence and never a mismatch.
func treehouseStatus(worktreePath string) ([]statusEntry, string) {
	cmd := exec.Command("treehouse", "status", "--json")
	cmd.Dir = worktreePath
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Sprintf("treehouse is not executable: %v", err)
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			return nil, fmt.Sprintf("treehouse status failed: %v", err)
		}
		return nil, fmt.Sprintf("treehouse status failed: %v: %s", err, detail)
	}
	var entries []statusEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Sprintf("treehouse status output is not a JSON array: %v", err)
	}
	return entries, ""
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
