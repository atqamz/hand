// Package worktree wraps treehouse worktree acquisition and the spawn collision guard.
package worktree

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	gitrepo "github.com/atqamz/hand/internal/git"
	"github.com/atqamz/hand/internal/secondhand"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toolchain"
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
	Path        string `json:"path"`
	Status      string `json:"status"`
	LeaseID     string `json:"lease_id"`
	LeaseHolder string `json:"lease_holder"`
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
func ObserveLease(clonePath, worktreePath, expectedLeaseID string) LeaseObservation {
	if worktreePath == "" {
		return LeaseObservation{State: LeaseAbsent}
	}
	if _, err := os.Stat(worktreePath); err != nil {
		if os.IsNotExist(err) {
			return LeaseObservation{State: LeaseAbsent}
		}
		return unknownLease(worktreePath, fmt.Sprintf("inspect worktree path: %v", err))
	}
	entries, reason := treehouseStatus(clonePath, worktreePath)
	if reason != "" {
		return unknownLease(worktreePath, reason)
	}
	for _, entry := range entries {
		if !gitrepo.SamePath(entry.Path, worktreePath) {
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
	out, _, err := runCore("git", worktreePath, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "", fmt.Errorf("git worktree status failed: %w", err)
	}
	if len(out) != 0 {
		return Dirty, nil
	}
	return Clean, nil
}

type CommitSafetyState string

const (
	CommitSafetyRemoteObserved CommitSafetyState = "remote-observed"
	CommitSafetyLocalOnly      CommitSafetyState = "local-only"
	CommitSafetyUnknown        CommitSafetyState = "unknown"
)

// CommitSafetyProbe records how HEAD was compared against remote-tracking refs. RemoteRefs is
// load-bearing: with none configured there is nothing to compare against, so a zero LocalOnly
// would record "the question was never asked" as if it meant "nothing is at risk".
type CommitSafetyProbe struct {
	Command    string
	WorkingDir string
	Reason     string
	Head       string
	LocalOnly  int
	RemoteRefs int
}

type CommitSafetyObservation struct {
	State CommitSafetyState
	Probe CommitSafetyProbe
}

// A remote-tracking ref exists only as this clone's record of a ref observed on a remote, so every
// commit this count reaches is one a remote was seen holding: zero is positive proof the work
// outlives the worktree. Exported for internal/runtime/pr.go's pull-request absence proof (atqamz/hand#428).
const LocalOnlyCommitCommand = "git rev-list --count HEAD --not --remotes"

// ObserveCommitSafety reports whether returning a worktree could discard commits held nowhere else
// and never fails: every cause that stops the comparison being made is CommitSafetyUnknown carrying
// its probe, because an observation that could not be made is not proof that the work is safe.
func ObserveCommitSafety(worktreePath string) CommitSafetyObservation {
	if worktreePath == "" {
		return unknownCommitSafety(CommitSafetyProbe{}, "no worktree path is recorded")
	}
	probe := CommitSafetyProbe{Command: LocalOnlyCommitCommand, WorkingDir: worktreePath}
	head, err := HeadCommit(worktreePath)
	if err != nil {
		return unknownCommitSafety(probe, err.Error())
	}
	probe.Head = head
	remotes, err := gitLines(worktreePath, "for-each-ref", "--format=%(refname)", "refs/remotes")
	if err != nil {
		return unknownCommitSafety(probe, fmt.Sprintf("list remote-tracking refs: %v", err))
	}
	probe.RemoteRefs = len(remotes)
	count, err := LocalOnlyCommitCount(worktreePath)
	if err != nil {
		return unknownCommitSafety(probe, err.Error())
	}
	probe.LocalOnly = count
	if count == 0 && probe.RemoteRefs > 0 {
		return CommitSafetyObservation{State: CommitSafetyRemoteObserved, Probe: probe}
	}
	if probe.RemoteRefs == 0 {
		return unknownCommitSafety(probe, "the clone holds no remote-tracking ref, so no commit here can be compared against one")
	}
	if upstream, pruned := prunedUpstream(worktreePath); pruned {
		return unknownCommitSafety(probe, fmt.Sprintf("the branch records upstream %s and no remote-tracking ref for it survives, so what the remote holds is no longer recorded here", upstream))
	}
	return CommitSafetyObservation{State: CommitSafetyLocalOnly, Probe: probe}
}

func unknownCommitSafety(probe CommitSafetyProbe, reason string) CommitSafetyObservation {
	if probe.Command == "" {
		probe.Command = LocalOnlyCommitCommand
	}
	probe.Reason = reason
	return CommitSafetyObservation{State: CommitSafetyUnknown, Probe: probe}
}

func LocalOnlyCommitCount(worktreePath string) (int, error) {
	out, err := gitOutput(worktreePath, "rev-list", "--count", "HEAD", "--not", "--remotes")
	if err != nil {
		return 0, fmt.Errorf("count commits held only in this worktree: %w", err)
	}
	count, convErr := strconv.Atoi(out)
	if convErr != nil {
		return 0, fmt.Errorf("count commits held only in this worktree: unreadable count %q", out)
	}
	return count, nil
}

func prunedUpstream(worktreePath string) (string, bool) {
	branch, err := gitOutput(worktreePath, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || branch == "" {
		return "", false
	}
	remote, err := gitOutput(worktreePath, "config", "--get", "branch."+branch+".remote")
	if err != nil || remote == "" {
		return "", false
	}
	merge, err := gitOutput(worktreePath, "config", "--get", "branch."+branch+".merge")
	if err != nil {
		return "", false
	}
	upstream := remote + "/" + strings.TrimPrefix(merge, "refs/heads/")
	if _, err := gitOutput(worktreePath, "rev-parse", "--verify", "--quiet", "refs/remotes/"+upstream+"^{commit}"); err != nil {
		return upstream, true
	}
	return "", false
}

func gitOutput(worktreePath string, args ...string) (string, error) {
	out, _, err := runCore("git", worktreePath, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func gitLines(worktreePath string, args ...string) ([]string, error) {
	out, err := gitOutput(worktreePath, args...)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// Get acquires a worktree from the project clone's treehouse pool. clonePath must be the project
// clone directory, because treehouse resolves the pool from its own working directory.
func Get(clonePath, leaseHolder string) (Lease, error) {
	args := []string{"get", "--lease", "--json"}
	if leaseHolder != "" {
		args = append(args, "--lease-holder", leaseHolder)
	}
	args, err := withTreehouseRoot(clonePath, "", args...)
	if err != nil {
		return Lease{}, err
	}
	out, stderr, err := runCore("treehouse", clonePath, args...)
	// Banners land on stderr ahead of the JSON, so the payload has to be read from stdout alone -
	// CombinedOutput here corrupts every parse (atqamz/hand#21).
	if err != nil {
		return Lease{}, fmt.Errorf("treehouse get failed: %w: %s", err, strings.TrimSpace(string(stderr)))
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
	lease := Lease{Path: payload.Path, ID: payload.LeaseID}
	if _, err := os.Stat(lease.Path); err == nil {
		actual, err := gitrepo.CommonDir(lease.Path)
		if err != nil {
			_ = ReturnLease(clonePath, lease.Path, lease.ID, true)
			return Lease{}, fmt.Errorf("treehouse get returned an unreadable worktree: %w", err)
		}
		expected := filepath.Join(clonePath, ".git")
		if !gitrepo.SamePath(expected, actual) {
			_ = ReturnLease(clonePath, lease.Path, lease.ID, true)
			return Lease{}, fmt.Errorf("treehouse get returned worktree rooted in another Git repository: got %s, want %s", actual, expected)
		}
	}
	return lease, nil
}

// Reads the full commit object ID currently checked out by a leased worktree.
func HeadCommit(worktreePath string) (string, error) {
	out, _, err := runCore("git", worktreePath, "rev-parse", "--verify", "HEAD^{commit}")
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
func Return(clonePath, worktreePath string, force bool) error {
	if worktreePath == "" {
		return nil
	}
	args := []string{"return", worktreePath}
	if force {
		args = append(args, "--force")
	}
	args, err := withTreehouseRoot(clonePath, worktreePath, args...)
	if err != nil {
		return err
	}
	return returnTreehouse(worktreePath, args, force)
}

// ReturnLease releases a worktree only when treehouse still owns the expected lease.
func ReturnLease(clonePath, worktreePath, leaseID string, force bool) error {
	if worktreePath == "" {
		return nil
	}
	if leaseID == "" {
		return Return(clonePath, worktreePath, force)
	}
	args := []string{"return"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, "--if-lease-id", leaseID, worktreePath)
	args, err := withTreehouseRoot(clonePath, worktreePath, args...)
	if err != nil {
		return err
	}
	return returnTreehouse(worktreePath, args, force)
}

func returnTreehouse(worktreePath string, args []string, force bool) error {
	stdout, stderr, err := runCore("treehouse", "", args...)
	out := append(stdout, stderr...)
	if err != nil {
		return fmt.Errorf("treehouse return failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	if !force && strings.Contains(string(out), "Aborted") {
		return &returnAbortedError{message: fmt.Sprintf("treehouse return aborted, worktree %s is still leased: %s", worktreePath, strings.TrimSpace(string(out)))}
	}
	return nil
}

// PoolEntry is one slot as treehouse status reports it, independent of any task Hand has a
// record of - a leased slot Hand never provisioned still appears here.
type PoolEntry struct {
	Path        string
	Status      string
	LeaseID     string
	LeaseHolder string
}

// PoolStatus lists every slot in clonePath's worktree pool, leased or not. It names no recorded
// worktree, so unlike ObserveLease it always resolves the pool atqamz/hand#427 put outside the
// clone, and it runs from clonePath rather than an ambient working directory.
func PoolStatus(clonePath string) ([]PoolEntry, error) {
	args, err := withTreehouseRoot(clonePath, "", "status", "--json")
	if err != nil {
		return nil, err
	}
	out, stderr, err := runCore("treehouse", clonePath, args...)
	if err != nil {
		detail := strings.TrimSpace(string(stderr))
		if detail == "" {
			return nil, fmt.Errorf("treehouse status failed: %w", err)
		}
		return nil, fmt.Errorf("treehouse status failed: %w: %s", err, detail)
	}
	var entries []statusEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("treehouse status output is not a JSON array: %w", err)
	}
	result := make([]PoolEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, PoolEntry(entry))
	}
	return result, nil
}

// ParseLeaseHolder splits a slot's lease_holder into the Fleet and Task identity a Hand-taken
// lease encodes, "hand:<fleet-id>:<task-id>" (see internal/runtime/provision.go). ok is false for
// anything that does not fit this shape, including a lease Hand never took.
func ParseLeaseHolder(holder string) (fleetID, taskID string, ok bool) {
	parts := strings.SplitN(holder, ":", 3)
	if len(parts) != 3 || parts[0] != "hand" || parts[1] == "" || parts[2] == "" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// Reports the pool entries, or the reason the pool could not be observed. A missing executable, a
// non-zero exit and unparsable output are all unobservability, never absence and never a mismatch.
func treehouseStatus(clonePath, worktreePath string) ([]statusEntry, string) {
	args, err := withTreehouseRoot(clonePath, worktreePath, "status", "--json")
	if err != nil {
		return nil, err.Error()
	}
	out, stderr, err := runCore("treehouse", worktreePath, args...)
	if err != nil {
		if strings.Contains(err.Error(), "executable file not found") {
			return nil, fmt.Sprintf("treehouse is not executable: %v", err)
		}
		detail := strings.TrimSpace(string(stderr))
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

func withTreehouseRoot(clonePath, recordedWorktreePath string, args ...string) ([]string, error) {
	root, err := poolRoot(clonePath, recordedWorktreePath)
	if err != nil {
		return nil, err
	}
	return append([]string{"--root", root}, args...), nil
}

// PoolsRoot is the directory every clone's worktree pool lives under - the one path that identifies
// any slot Hand's own pools could ever create. Exported for cmd/init.go's managed-tree refusal
// (atqamz/hand#413): no Fleet home may be registered inside it.
func PoolsRoot() (string, error) {
	infra, err := secondhand.Home()
	if err != nil {
		return "", fmt.Errorf("resolve worktree pool root: %w", err)
	}
	return filepath.Join(infra, "pools"), nil
}

// Where this clone's pool lives. Before atqamz/hand#427 it was the clone itself, which put every
// worker worktree under the fleet home and fed the worker the supervisor's own CLAUDE.md. A digest
// of the clone path keys it instead: one pool per fleet home and project, outside every home.
func poolRoot(clonePath, recordedWorktreePath string) (string, error) {
	if recordedWorktreePath != "" && withinPath(clonePath, recordedWorktreePath) {
		return clonePath, nil
	}
	root, err := PoolsRoot()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(filepath.Clean(clonePath)))
	return filepath.Join(root, fmt.Sprintf("%s-%x", filepath.Base(clonePath), digest[:6])), nil
}

// Reports whether path is root or sits inside it, comparing the recorded strings without resolving
// symlinks: a worktree recorded under the clone was leased before atqamz/hand#427 and its pool is
// still the clone, so it stays observable and returnable.
func withinPath(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func runCore(tool, dir string, args ...string) ([]byte, []byte, error) {
	managed, err := toolchain.Resolve()
	if err != nil {
		stdout, stderr, legacyErr := toolchain.RunLegacyForTests(context.Background(), tool, dir, args...)
		if legacyErr == nil {
			return stdout, stderr, nil
		}
		return stdout, stderr, fmt.Errorf("resolve managed %s: %w; legacy test command: %v", tool, err, legacyErr)
	}
	var path string
	switch tool {
	case "git":
		path = managed.GitPath
	case "treehouse":
		path = managed.TreehousePath
	default:
		return nil, nil, fmt.Errorf("unsupported core tool %q", tool)
	}
	spec, err := managed.Process(path, args...)
	if err != nil {
		return nil, nil, err
	}
	spec.Dir = dir
	var stdout, stderr bytes.Buffer
	spec.Stdout = &stdout
	spec.Stderr = &stderr
	err = spec.Run(context.Background())
	return stdout.Bytes(), stderr.Bytes(), err
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
		if gitrepo.SamePath(attempt.Worktree, lease.Path) {
			return history.Task.ID, nil
		}
	}
	return "", nil
}
