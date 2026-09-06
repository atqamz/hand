package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	handgit "github.com/atqamz/hand/internal/git"
)

type canonicalV19GitWorktreeRemoveObservation struct {
	State                  canonicalV19GitWorktreeObservationState
	Locked                 bool
	PrivateGitDir          string
	LockReason             string
	HeadRevision           string
	PhysicalIdentityDigest string
	StatusDigest           string
	CleanCandidatesDigest  string
	StableRefCount         int
	LocalOnlyCommitCount   int
	EvidenceDigest         string
	Reason                 string
}

type canonicalV19WorktreeRemoveNativeDeps struct {
	observe func(string, CanonicalV19WorktreeRemoveRequest) canonicalV19GitWorktreeRemoveObservation
	perform func(string, CanonicalV19WorktreeRemoveRequest) error
	now     func() time.Time
}

// ReconcileCanonicalV19WorktreeRemove observes one exact native Git removal
// and mutates only after this call durably submits the prepared operation.
func ReconcileCanonicalV19WorktreeRemove(ctx context.Context, homeDir, operationID string) (string, error) {
	return reconcileCanonicalV19WorktreeRemove(ctx, homeDir, operationID, canonicalV19WorktreeRemoveNativeDeps{
		observe: observeCanonicalV19GitWorktreeRemove,
		perform: performCanonicalV19GitWorktreeRemove,
		now:     time.Now,
	})
}

func reconcileCanonicalV19WorktreeRemove(
	ctx context.Context,
	homeDir string,
	operationID string,
	deps canonicalV19WorktreeRemoveNativeDeps,
) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if operationID == "" {
		return "", fmt.Errorf("reconcile canonical v19 WorktreeRemove: operation ID is empty")
	}
	if deps.observe == nil || deps.perform == nil || deps.now == nil {
		return "", fmt.Errorf("reconcile canonical v19 WorktreeRemove: native adapter dependencies are incomplete")
	}

	current, err := readCanonicalV19WorktreeRemoveCurrent(ctx, homeDir, operationID)
	if err != nil {
		return "", fmt.Errorf("reconcile canonical v19 WorktreeRemove: %w", err)
	}
	switch current.State {
	case "succeeded", "rejected", "no-effect":
		return current.State, nil
	case "prepared", "submitted", "uncertain":
	default:
		return current.State, fmt.Errorf("reconcile canonical v19 WorktreeRemove: %w: operation %q is %q",
			ErrCanonicalV19WorktreeRemoveTransition, operationID, current.State)
	}

	observed := deps.observe(homeDir, current.Request)
	if current.State != "prepared" {
		return reconcileCanonicalV19SubmittedWorktreeRemove(ctx, homeDir, current, observed, deps.now)
	}

	switch observed.State {
	case canonicalV19GitWorktreeAbsent:
		return completeCanonicalV19ObservedWorktreeRemove(ctx, homeDir, current, observed, deps.now)
	case canonicalV19GitWorktreeExact:
	case canonicalV19GitWorktreeMismatch:
		return current.State, fmt.Errorf("reconcile canonical v19 WorktreeRemove: exact ownership or preservation is not proven: %s", observed.Reason)
	case canonicalV19GitWorktreeUnknown:
		return current.State, fmt.Errorf("reconcile canonical v19 WorktreeRemove: native Git observation is unknown: %s", observed.Reason)
	default:
		return current.State, fmt.Errorf("reconcile canonical v19 WorktreeRemove: native Git observation state %q is invalid", observed.State)
	}

	submittedAt := canonicalV19WorktreeRemoveTimestampAfter(deps.now(), current.StateChangedAt)
	request, err := SubmitCanonicalV19WorktreeRemove(ctx, homeDir, operationID, submittedAt, observed.EvidenceDigest)
	if err != nil {
		return current.State, err
	}
	current.Request = request
	current.State = "submitted"
	current.StateChangedAt = submittedAt

	observed = deps.observe(homeDir, request)
	switch observed.State {
	case canonicalV19GitWorktreeAbsent:
		return completeCanonicalV19ObservedWorktreeRemove(ctx, homeDir, current, observed, deps.now)
	case canonicalV19GitWorktreeExact:
	case canonicalV19GitWorktreeMismatch, canonicalV19GitWorktreeUnknown:
		return classifyCanonicalV19ObservedWorktreeRemoveUncertain(ctx, homeDir, current, observed, deps.now, nil)
	default:
		return current.State, fmt.Errorf("reconcile canonical v19 WorktreeRemove: native Git observation state %q is invalid", observed.State)
	}

	performErr := deps.perform(homeDir, request)
	observed = deps.observe(homeDir, request)
	switch observed.State {
	case canonicalV19GitWorktreeAbsent:
		return completeCanonicalV19ObservedWorktreeRemove(ctx, homeDir, current, observed, deps.now)
	case canonicalV19GitWorktreeExact:
		return classifyCanonicalV19ObservedWorktreeRemoveNoEffect(ctx, homeDir, current, observed, deps.now,
			canonicalV19WorktreeRemovePerformFailure("native Git remove left the exact worktree unchanged", performErr))
	case canonicalV19GitWorktreeMismatch, canonicalV19GitWorktreeUnknown:
		return classifyCanonicalV19ObservedWorktreeRemoveUncertain(ctx, homeDir, current, observed, deps.now, performErr)
	default:
		return current.State, fmt.Errorf("reconcile canonical v19 WorktreeRemove: native Git observation state %q is invalid", observed.State)
	}
}

func reconcileCanonicalV19SubmittedWorktreeRemove(
	ctx context.Context,
	homeDir string,
	current canonicalV19WorktreeRemoveCurrent,
	observed canonicalV19GitWorktreeRemoveObservation,
	now func() time.Time,
) (string, error) {
	switch observed.State {
	case canonicalV19GitWorktreeAbsent:
		return completeCanonicalV19ObservedWorktreeRemove(ctx, homeDir, current, observed, now)
	case canonicalV19GitWorktreeExact:
		return classifyCanonicalV19ObservedWorktreeRemoveNoEffect(ctx, homeDir, current, observed, now,
			"positive observation proves the submitted remove left the exact worktree unchanged")
	case canonicalV19GitWorktreeMismatch, canonicalV19GitWorktreeUnknown:
		if current.State == "submitted" {
			return classifyCanonicalV19ObservedWorktreeRemoveUncertain(ctx, homeDir, current, observed, now, nil)
		}
		return "uncertain", fmt.Errorf("reconcile canonical v19 WorktreeRemove: operation remains uncertain: %s", observed.Reason)
	default:
		return current.State, fmt.Errorf("reconcile canonical v19 WorktreeRemove: native Git observation state %q is invalid", observed.State)
	}
}

func completeCanonicalV19ObservedWorktreeRemove(
	ctx context.Context,
	homeDir string,
	current canonicalV19WorktreeRemoveCurrent,
	observed canonicalV19GitWorktreeRemoveObservation,
	now func() time.Time,
) (string, error) {
	removedAt := canonicalV19WorktreeRemoveTimestampAfter(now(), current.StateChangedAt)
	if err := CompleteCanonicalV19WorktreeRemove(ctx, homeDir, CanonicalV19WorktreeRemovedEvidence{
		OperationID:    current.Request.OperationID,
		RemovedAt:      removedAt,
		EvidenceDigest: observed.EvidenceDigest,
	}); err != nil {
		return current.State, err
	}
	return "succeeded", nil
}

func classifyCanonicalV19ObservedWorktreeRemoveNoEffect(
	ctx context.Context,
	homeDir string,
	current canonicalV19WorktreeRemoveCurrent,
	observed canonicalV19GitWorktreeRemoveObservation,
	now func() time.Time,
	reason string,
) (string, error) {
	observedAt := canonicalV19WorktreeRemoveTimestampAfter(now(), current.StateChangedAt)
	if err := ClassifyCanonicalV19WorktreeRemove(ctx, homeDir, CanonicalV19WorktreeRemoveTransitionInput{
		OperationID:    current.Request.OperationID,
		State:          "no-effect",
		ObservedAt:     observedAt,
		EvidenceDigest: observed.EvidenceDigest,
	}); err != nil {
		return current.State, err
	}
	if observed.Reason != "" {
		reason += ": " + observed.Reason
	}
	return "no-effect", fmt.Errorf("reconcile canonical v19 WorktreeRemove: %s", reason)
}

func classifyCanonicalV19ObservedWorktreeRemoveUncertain(
	ctx context.Context,
	homeDir string,
	current canonicalV19WorktreeRemoveCurrent,
	observed canonicalV19GitWorktreeRemoveObservation,
	now func() time.Time,
	performErr error,
) (string, error) {
	if current.State == "uncertain" {
		return "uncertain", fmt.Errorf("reconcile canonical v19 WorktreeRemove: operation remains uncertain: %s", observed.Reason)
	}
	observedAt := canonicalV19WorktreeRemoveTimestampAfter(now(), current.StateChangedAt)
	if err := ClassifyCanonicalV19WorktreeRemove(ctx, homeDir, CanonicalV19WorktreeRemoveTransitionInput{
		OperationID:    current.Request.OperationID,
		State:          "uncertain",
		ObservedAt:     observedAt,
		EvidenceDigest: observed.EvidenceDigest,
	}); err != nil {
		return current.State, err
	}
	reason := observed.Reason
	if performErr != nil {
		reason = canonicalV19WorktreeRemovePerformFailure(reason, performErr)
	}
	return "uncertain", fmt.Errorf("reconcile canonical v19 WorktreeRemove: operation is uncertain: %s", reason)
}

func readCanonicalV19WorktreeRemoveCurrent(ctx context.Context, homeDir, operationID string) (canonicalV19WorktreeRemoveCurrent, error) {
	db, err := openReadOnly(homeDir)
	if err != nil {
		return canonicalV19WorktreeRemoveCurrent{}, err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19WorktreeRemoveCurrent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	return loadCanonicalV19WorktreeRemoveCurrent(ctx, tx, operationID)
}

func observeCanonicalV19GitWorktreeRemove(homeDir string, request CanonicalV19WorktreeRemoveRequest) canonicalV19GitWorktreeRemoveObservation {
	return observeCanonicalV19GitWorktreeRemoveLockState(homeDir, request, true)
}

func observeCanonicalV19GitWorktreeRemoveLockState(
	homeDir string,
	request CanonicalV19WorktreeRemoveRequest,
	expectLocked bool,
) canonicalV19GitWorktreeRemoveObservation {
	repositoryPath := canonicalV19ObservedPath(homeDir, request.RepositoryLocator)
	expectedCommonDir := canonicalV19ObservedPath(homeDir, request.ExpectedCommonGitDir)
	expectedPrivateGitDir := canonicalV19ObservedPath(homeDir, request.ExpectedPrivateGitDir)

	root, err := handgit.ResolveRoot(repositoryPath)
	if err != nil {
		return canonicalV19UnknownGitWorktreeRemove(request, fmt.Sprintf("resolve repository root: %v", err))
	}
	if !handgit.SamePath(root, repositoryPath) {
		return canonicalV19UnknownGitWorktreeRemove(request, "repository locator no longer resolves to its exact root")
	}
	commonDir, err := handgit.CommonDir(repositoryPath)
	if err != nil {
		return canonicalV19UnknownGitWorktreeRemove(request, fmt.Sprintf("resolve common Git directory: %v", err))
	}
	if !handgit.SamePath(commonDir, expectedCommonDir) {
		return canonicalV19UnknownGitWorktreeRemove(request, "WorkspaceBinding common Git directory no longer matches")
	}

	listing, err := handgit.Run(repositoryPath, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return canonicalV19UnknownGitWorktreeRemove(request, fmt.Sprintf("list registered Git worktrees: %v", err))
	}
	records, err := parseCanonicalV19GitWorktreeList(listing)
	if err != nil {
		return canonicalV19UnknownGitWorktreeRemove(request, err.Error())
	}
	matches := make([]canonicalV19GitWorktreeRecord, 0, 1)
	for _, record := range records {
		if handgit.SamePath(filepath.FromSlash(record.Path), request.Path) {
			matches = append(matches, record)
		}
	}
	if len(matches) > 1 {
		return canonicalV19UnknownGitWorktreeRemove(request, "Git reports the exact worktree path more than once")
	}
	if len(matches) == 0 {
		if _, err := os.Stat(request.Path); err == nil {
			return canonicalV19FinalizeGitWorktreeRemoveObservation(request, canonicalV19GitWorktreeRemoveObservation{
				State:  canonicalV19GitWorktreeMismatch,
				Reason: "worktree path still exists without the expected registration",
			})
		} else if !os.IsNotExist(err) {
			return canonicalV19UnknownGitWorktreeRemove(request, fmt.Sprintf("inspect worktree path: %v", err))
		}
		if _, err := os.Stat(expectedPrivateGitDir); err == nil {
			return canonicalV19FinalizeGitWorktreeRemoveObservation(request, canonicalV19GitWorktreeRemoveObservation{
				State:  canonicalV19GitWorktreeMismatch,
				Reason: "private Git administration directory remains after registration disappeared",
			})
		} else if !os.IsNotExist(err) {
			return canonicalV19UnknownGitWorktreeRemove(request, fmt.Sprintf("inspect private Git directory: %v", err))
		}
		return canonicalV19FinalizeGitWorktreeRemoveObservation(request, canonicalV19GitWorktreeRemoveObservation{
			State: canonicalV19GitWorktreeAbsent,
		})
	}

	record := matches[0]
	observed := canonicalV19GitWorktreeRemoveObservation{
		Locked:       record.Locked,
		LockReason:   record.LockReason,
		HeadRevision: record.HeadRevision,
	}
	if record.Bare || record.Prunable || !record.Detached || record.HeadRevision != request.ExpectedHeadRevision {
		observed.State = canonicalV19GitWorktreeMismatch
		observed.Reason = "registered worktree metadata no longer matches the exact detached HEAD binding"
		return canonicalV19FinalizeGitWorktreeRemoveObservation(request, observed)
	}
	if expectLocked {
		if !record.Locked || record.LockReason != request.ExpectedLockReason {
			observed.State = canonicalV19GitWorktreeMismatch
			observed.Reason = "registered worktree lock ownership no longer matches the exact binding"
			return canonicalV19FinalizeGitWorktreeRemoveObservation(request, observed)
		}
	} else if record.Locked {
		observed.State = canonicalV19GitWorktreeMismatch
		observed.Reason = "worktree remained locked after the authorized unlock mutation"
		return canonicalV19FinalizeGitWorktreeRemoveObservation(request, observed)
	}

	info, err := os.Stat(request.Path)
	if err != nil {
		observed.State = canonicalV19GitWorktreeMismatch
		observed.Reason = fmt.Sprintf("registered worktree path cannot be inspected: %v", err)
		return canonicalV19FinalizeGitWorktreeRemoveObservation(request, observed)
	}
	if !info.IsDir() {
		observed.State = canonicalV19GitWorktreeMismatch
		observed.Reason = "registered worktree path is not a directory"
		return canonicalV19FinalizeGitWorktreeRemoveObservation(request, observed)
	}
	worktreeRoot, err := handgit.ResolveRoot(request.Path)
	if err != nil || !handgit.SamePath(worktreeRoot, request.Path) {
		observed.State = canonicalV19GitWorktreeMismatch
		observed.Reason = "registered worktree no longer resolves to its exact root"
		return canonicalV19FinalizeGitWorktreeRemoveObservation(request, observed)
	}
	worktreeCommonDir, err := handgit.CommonDir(request.Path)
	if err != nil || !handgit.SamePath(worktreeCommonDir, expectedCommonDir) {
		observed.State = canonicalV19GitWorktreeMismatch
		observed.Reason = "registered worktree common Git directory no longer matches"
		return canonicalV19FinalizeGitWorktreeRemoveObservation(request, observed)
	}
	privateOutput, err := handgit.Run(request.Path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return canonicalV19UnknownGitWorktreeRemove(request, fmt.Sprintf("resolve private Git directory: %v", err))
	}
	observed.PrivateGitDir = filepath.Clean(filepath.FromSlash(strings.TrimSpace(privateOutput)))
	if !handgit.SamePath(observed.PrivateGitDir, expectedPrivateGitDir) {
		observed.State = canonicalV19GitWorktreeMismatch
		observed.Reason = "private Git administration identity no longer matches the exact binding"
		return canonicalV19FinalizeGitWorktreeRemoveObservation(request, observed)
	}
	headRevision, err := handgit.HeadCommit(request.Path)
	if err != nil {
		return canonicalV19UnknownGitWorktreeRemove(request, fmt.Sprintf("resolve exact worktree HEAD: %v", err))
	}
	observed.HeadRevision = headRevision
	if headRevision != request.ExpectedHeadRevision {
		observed.State = canonicalV19GitWorktreeMismatch
		observed.Reason = "worktree HEAD changed from the exact persisted binding"
		return canonicalV19FinalizeGitWorktreeRemoveObservation(request, observed)
	}
	finalInfo, err := os.Stat(request.Path)
	if err != nil || !os.SameFile(info, finalInfo) {
		return canonicalV19UnknownGitWorktreeRemove(request, "worktree physical identity changed during observation")
	}
	physicalIdentity, err := canonicalV19WorktreePhysicalIdentity(request.Path, finalInfo)
	if err != nil {
		return canonicalV19UnknownGitWorktreeRemove(request, fmt.Sprintf("capture worktree physical identity: %v", err))
	}
	observed.PhysicalIdentityDigest = canonicalV19WorktreePhysicalIdentityDigest(physicalIdentity)
	if observed.PhysicalIdentityDigest != request.ExpectedPhysicalIdentityDigest {
		observed.State = canonicalV19GitWorktreeMismatch
		observed.Reason = "worktree physical identity no longer matches the exact persisted binding"
		return canonicalV19FinalizeGitWorktreeRemoveObservation(request, observed)
	}

	status, err := handgit.Run(request.Path, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return canonicalV19UnknownGitWorktreeRemove(request, fmt.Sprintf("observe worktree cleanliness: %v", err))
	}
	observed.StatusDigest = canonicalV19WorktreeRemoveTextDigest(status)
	if strings.TrimSpace(status) != "" {
		observed.State = canonicalV19GitWorktreeMismatch
		observed.Reason = "worktree has uncommitted or untracked content; preservation is unsafe"
		return canonicalV19FinalizeGitWorktreeRemoveObservation(request, observed)
	}
	cleanCandidates, err := handgit.Run(request.Path, "clean", "-ndx")
	if err != nil {
		return canonicalV19UnknownGitWorktreeRemove(request, fmt.Sprintf("observe ignored/untracked cleanup candidates: %v", err))
	}
	observed.CleanCandidatesDigest = canonicalV19WorktreeRemoveTextDigest(cleanCandidates)
	if strings.TrimSpace(cleanCandidates) != "" {
		observed.State = canonicalV19GitWorktreeMismatch
		observed.Reason = "worktree contains ignored or untracked filesystem content; preservation is unsafe"
		return canonicalV19FinalizeGitWorktreeRemoveObservation(request, observed)
	}
	gitlinks, err := handgit.Run(request.Path, "ls-files", "--stage")
	if err != nil {
		return canonicalV19UnknownGitWorktreeRemove(request, fmt.Sprintf("inspect worktree gitlinks: %v", err))
	}
	for _, line := range strings.Split(gitlinks, "\n") {
		if strings.HasPrefix(line, "160000 ") {
			observed.State = canonicalV19GitWorktreeMismatch
			observed.Reason = "worktree contains submodules; non-force native removal is not preservation-safe"
			return canonicalV19FinalizeGitWorktreeRemoveObservation(request, observed)
		}
	}
	stableRefs, err := handgit.Run(request.Path, "for-each-ref", "--format=%(refname)", "--contains", request.ExpectedHeadRevision,
		"refs/heads", "refs/tags", "refs/remotes")
	if err != nil {
		return canonicalV19UnknownGitWorktreeRemove(request, fmt.Sprintf("observe stable refs containing exact HEAD: %v", err))
	}
	observed.StableRefCount = canonicalV19NonemptyLineCount(stableRefs)
	if observed.StableRefCount == 0 {
		return canonicalV19UnknownGitWorktreeRemove(request, "no stable common Git ref positively preserves the exact worktree HEAD")
	}
	localOnly, err := handgit.Run(request.Path, "rev-list", "--count", "HEAD", "--not", "--branches", "--tags", "--remotes")
	if err != nil {
		return canonicalV19UnknownGitWorktreeRemove(request, fmt.Sprintf("compare exact HEAD with stable common refs: %v", err))
	}
	observed.LocalOnlyCommitCount, err = strconv.Atoi(strings.TrimSpace(localOnly))
	if err != nil {
		return canonicalV19UnknownGitWorktreeRemove(request, fmt.Sprintf("parse local-only commit count %q: %v", localOnly, err))
	}
	if observed.LocalOnlyCommitCount != 0 {
		observed.State = canonicalV19GitWorktreeMismatch
		observed.Reason = "worktree HEAD contains commits not preserved by a stable common Git ref"
		return canonicalV19FinalizeGitWorktreeRemoveObservation(request, observed)
	}

	observed.State = canonicalV19GitWorktreeExact
	return canonicalV19FinalizeGitWorktreeRemoveObservation(request, observed)
}

func performCanonicalV19GitWorktreeRemove(homeDir string, request CanonicalV19WorktreeRemoveRequest) error {
	repositoryPath := canonicalV19ObservedPath(homeDir, request.RepositoryLocator)
	if _, err := handgit.Run(repositoryPath, "worktree", "unlock", request.Path); err != nil {
		return fmt.Errorf("git worktree unlock: %w", err)
	}
	observed := observeCanonicalV19GitWorktreeRemoveLockState(homeDir, request, false)
	if observed.State != canonicalV19GitWorktreeExact {
		return fmt.Errorf("post-unlock exact safety recheck failed: %s (%s)", observed.State, observed.Reason)
	}
	if _, err := handgit.Run(repositoryPath, "worktree", "remove", request.Path); err != nil {
		return fmt.Errorf("git worktree remove: %w", err)
	}
	return nil
}

func canonicalV19UnknownGitWorktreeRemove(request CanonicalV19WorktreeRemoveRequest, reason string) canonicalV19GitWorktreeRemoveObservation {
	return canonicalV19FinalizeGitWorktreeRemoveObservation(request, canonicalV19GitWorktreeRemoveObservation{
		State:  canonicalV19GitWorktreeUnknown,
		Reason: reason,
	})
}

func canonicalV19FinalizeGitWorktreeRemoveObservation(
	request CanonicalV19WorktreeRemoveRequest,
	observed canonicalV19GitWorktreeRemoveObservation,
) canonicalV19GitWorktreeRemoveObservation {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:git-worktree-remove-observation:v1")
	writeCanonicalV19DigestField(hash, "request_digest", request.RequestDigest)
	writeCanonicalV19DigestField(hash, "state", string(observed.State))
	writeCanonicalV19DigestField(hash, "locked", strconv.FormatBool(observed.Locked))
	writeCanonicalV19DigestField(hash, "private_git_dir", observed.PrivateGitDir)
	writeCanonicalV19DigestField(hash, "lock_reason", observed.LockReason)
	writeCanonicalV19DigestField(hash, "head_revision", observed.HeadRevision)
	writeCanonicalV19DigestField(hash, "physical_identity_digest", observed.PhysicalIdentityDigest)
	writeCanonicalV19DigestField(hash, "status_digest", observed.StatusDigest)
	writeCanonicalV19DigestField(hash, "clean_candidates_digest", observed.CleanCandidatesDigest)
	writeCanonicalV19DigestField(hash, "stable_ref_count", strconv.Itoa(observed.StableRefCount))
	writeCanonicalV19DigestField(hash, "local_only_commit_count", strconv.Itoa(observed.LocalOnlyCommitCount))
	writeCanonicalV19DigestField(hash, "reason", observed.Reason)
	observed.EvidenceDigest = hex.EncodeToString(hash.Sum(nil))
	return observed
}

func canonicalV19WorktreeRemoveTextDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func canonicalV19NonemptyLineCount(value string) int {
	count := 0
	for _, line := range strings.Split(value, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func canonicalV19WorktreeRemovePerformFailure(prefix string, err error) string {
	if err == nil {
		return prefix
	}
	if prefix == "" {
		return fmt.Sprintf("native Git remove failed: %v", err)
	}
	return fmt.Sprintf("%s; native Git remove failed: %v", prefix, err)
}

func canonicalV19WorktreeRemoveTimestampAfter(now time.Time, previous string) string {
	candidate := now.UTC()
	if prior, err := time.Parse(time.RFC3339Nano, previous); err == nil && !candidate.After(prior) {
		candidate = prior.Add(time.Nanosecond)
	}
	return candidate.Format(time.RFC3339Nano)
}
