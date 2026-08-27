// Package worktree wraps treehouse worktree acquisition and the spawn collision guard.
package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	gitrepo "github.com/atqamz/hand/internal/git"
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

type SlotSoundness struct {
	Sound       bool
	MetadataDir string
	CommonDir   string
	Failures    []string
}

type PoolEntry struct {
	Path    string
	Status  string
	LeaseID string
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
// outlives the worktree, established without a network call.
const localOnlyCommitCommand = "git rev-list --count HEAD --not --remotes"

// ObserveCommitSafety reports whether returning a worktree could discard commits held nowhere else
// and never fails: every cause that stops the comparison being made is CommitSafetyUnknown carrying
// its probe, because an observation that could not be made is not proof that the work is safe.
func ObserveCommitSafety(worktreePath string) CommitSafetyObservation {
	if worktreePath == "" {
		return unknownCommitSafety(CommitSafetyProbe{}, "no worktree path is recorded")
	}
	probe := CommitSafetyProbe{Command: localOnlyCommitCommand, WorkingDir: worktreePath}
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
	count, err := localOnlyCommitCount(worktreePath)
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
		probe.Command = localOnlyCommitCommand
	}
	probe.Reason = reason
	return CommitSafetyObservation{State: CommitSafetyUnknown, Probe: probe}
}

func localOnlyCommitCount(worktreePath string) (int, error) {
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
	args = withTreehouseRoot(clonePath, args...)
	const maxUnsoundLeases = 128
	seen := make(map[string]struct{})
	var retired []string
	for range maxUnsoundLeases {
		lease, err := getLease(clonePath, args)
		if err != nil {
			if len(retired) > 0 {
				return Lease{}, fmt.Errorf("%w; treehouse exhausted: %v", unsoundPoolError(clonePath, retired), err)
			}
			return Lease{}, err
		}
		pool, err := PoolStatus(clonePath)
		if err != nil {
			if !cloneOwnsWorktree(clonePath, lease.Path) {
				return Lease{}, fmt.Errorf("inspect treehouse pool after acquisition: %w; refusing to return an unverified lease path %s", err, lease.Path)
			}
			if returnErr := ReturnLease(clonePath, lease.Path, lease.ID, true); returnErr != nil {
				return Lease{}, fmt.Errorf("inspect treehouse pool after acquisition: %w; return lease: %v", err, returnErr)
			}
			return Lease{}, fmt.Errorf("inspect treehouse pool after acquisition: %w", err)
		}
		if !poolContains(pool, lease.Path) {
			return Lease{}, fmt.Errorf("treehouse returned worktree outside its reported pool: %s", lease.Path)
		}
		soundness := CheckSoundness(clonePath, lease.Path)
		if soundness.Sound {
			return lease, nil
		}
		key, _ := filepath.Abs(lease.Path)
		if _, ok := seen[key]; ok {
			_ = ReturnLease(clonePath, lease.Path, lease.ID, true)
			return Lease{}, unsoundPoolError(clonePath, append(retired, soundness.Failures...))
		}
		seen[key] = struct{}{}
		if err := ReturnLease(clonePath, lease.Path, lease.ID, true); err != nil {
			return Lease{}, fmt.Errorf("return unsound treehouse lease %s: %w", lease.Path, err)
		}
		foreign, err := retireSlot(clonePath, lease.Path, soundness)
		if err != nil {
			return Lease{}, err
		}
		retired = append(retired, fmt.Sprintf("%s: %s", lease.Path, strings.Join(soundness.Failures, ", ")))
		if foreign != "" {
			retired = append(retired, foreign)
		}
	}
	return Lease{}, unsoundPoolError(clonePath, retired)
}

func poolContains(pool []PoolEntry, path string) bool {
	for _, entry := range pool {
		if gitrepo.SamePath(entry.Path, path) {
			return true
		}
	}
	return false
}

func cloneOwnsWorktree(clonePath, worktreePath string) bool {
	info, err := os.Stat(filepath.Join(worktreePath, ".git"))
	if err != nil || info.IsDir() {
		return false
	}
	common, err := gitrepo.CommonDir(worktreePath)
	return err == nil && gitrepo.SamePath(common, filepath.Join(clonePath, ".git"))
}

func getLease(clonePath string, args []string) (Lease, error) {
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
	return Lease{Path: payload.Path, ID: payload.LeaseID}, nil
}

func unsoundPoolError(clonePath string, findings []string) error {
	return fmt.Errorf("treehouse pool for %s has no sound free slot: %s", clonePath, strings.Join(findings, "; "))
}

func ReadMetadataDir(worktreePath string) (string, error) {
	return gitrepo.ReadGitDirFile(filepath.Join(worktreePath, ".git"))
}

func CheckSoundness(clonePath, worktreePath string) SlotSoundness {
	result := SlotSoundness{}
	metadata, err := ReadMetadataDir(worktreePath)
	if err != nil {
		result.Failures = append(result.Failures, fmt.Sprintf(".git does not resolve: %v", err))
	} else {
		result.MetadataDir = metadata
		info, statErr := os.Stat(metadata)
		if os.IsNotExist(statErr) {
			result.Failures = append(result.Failures, "metadata directory does not exist")
		} else if statErr != nil {
			result.Failures = append(result.Failures, fmt.Sprintf("metadata directory cannot be inspected: %v", statErr))
		} else if !info.IsDir() {
			result.Failures = append(result.Failures, "metadata path is not a directory")
		} else {
			back, backErr := gitrepo.ReadWorktreeGitDir(filepath.Join(metadata, "gitdir"))
			if backErr != nil {
				result.Failures = append(result.Failures, fmt.Sprintf("metadata gitdir does not point back to this slot: %v", backErr))
			} else if !gitrepo.SamePath(back, filepath.Join(worktreePath, ".git")) {
				result.Failures = append(result.Failures, fmt.Sprintf("metadata gitdir does not point back to this slot: got %s", back))
			}
		}
	}
	common, commonErr := gitrepo.CommonDir(worktreePath)
	if commonErr != nil {
		result.Failures = append(result.Failures, fmt.Sprintf("common directory cannot be resolved: %v", commonErr))
	} else {
		result.CommonDir = common
		expected := filepath.Join(clonePath, ".git")
		if !gitrepo.SamePath(common, expected) {
			result.Failures = append(result.Failures, fmt.Sprintf("common directory is %s, want %s", common, expected))
		}
	}
	result.Sound = len(result.Failures) == 0
	return result
}

func RemoveMetadata(clonePath, worktreePath string) error {
	gitFile := filepath.Join(worktreePath, ".git")
	info, err := os.Stat(gitFile)
	if errors.Is(err, os.ErrNotExist) || (err == nil && info.IsDir()) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect worktree Git file: %w", err)
	}
	metadata, err := ReadMetadataDir(worktreePath)
	if err != nil {
		return fmt.Errorf("read worktree Git file: %w", err)
	}
	expected := filepath.Join(clonePath, ".git", "worktrees")
	rel, err := filepath.Rel(expected, metadata)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("refuse to remove worktree metadata outside the registered clone: %s", metadata)
	}
	if err := os.RemoveAll(metadata); err != nil {
		return fmt.Errorf("remove worktree metadata %s: %w", metadata, err)
	}
	return nil
}

func retireSlot(clonePath, worktreePath string, soundness SlotSoundness) (string, error) {
	if info, err := os.Stat(filepath.Join(worktreePath, ".git")); err == nil && info.IsDir() {
		return "", fmt.Errorf("refuse to retire Git repository root %s", worktreePath)
	}
	if err := os.RemoveAll(worktreePath); err != nil {
		return "", fmt.Errorf("retire unsound worktree %s: %w", worktreePath, err)
	}
	expected := filepath.Join(clonePath, ".git")
	if soundness.CommonDir != "" && !gitrepo.SamePath(soundness.CommonDir, expected) {
		return fmt.Sprintf("foreign Git registration for %s remains at %s; prune it by hand", worktreePath, soundness.CommonDir), nil
	}
	if soundness.CommonDir == "" {
		return "", nil
	}
	if _, err := gitrepo.Run(clonePath, "worktree", "prune"); err != nil {
		return "", fmt.Errorf("prune retired worktree metadata: %w", err)
	}
	return "", nil
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
	args = withTreehouseRoot(clonePath, args...)
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
	args = withTreehouseRoot(clonePath, args...)
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

// Reports the pool entries, or the reason the pool could not be observed. A missing executable, a
// non-zero exit and unparsable output are all unobservability, never absence and never a mismatch.
func treehouseStatus(clonePath, worktreePath string) ([]statusEntry, string) {
	out, stderr, err := runCore("treehouse", worktreePath, withTreehouseRoot(clonePath, "status", "--json")...)
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

func PoolStatus(clonePath string) ([]PoolEntry, error) {
	entries, reason := treehouseStatus(clonePath, clonePath)
	if reason != "" {
		return nil, errors.New(reason)
	}
	pool := make([]PoolEntry, 0, len(entries))
	for _, entry := range entries {
		pool = append(pool, PoolEntry(entry))
	}
	return pool, nil
}

func withTreehouseRoot(clonePath string, args ...string) []string {
	return append([]string{"--root", clonePath}, args...)
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
