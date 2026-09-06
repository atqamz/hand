package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	handgit "github.com/atqamz/hand/internal/git"
)

type canonicalV19GitWorktreeObservationState string

const (
	canonicalV19GitWorktreeExact    canonicalV19GitWorktreeObservationState = "exact"
	canonicalV19GitWorktreeAbsent   canonicalV19GitWorktreeObservationState = "absent"
	canonicalV19GitWorktreeMismatch canonicalV19GitWorktreeObservationState = "mismatch"
	canonicalV19GitWorktreeUnknown  canonicalV19GitWorktreeObservationState = "unknown"
)

type canonicalV19GitWorktreeObservation struct {
	State                  canonicalV19GitWorktreeObservationState
	PrivateGitDir          string
	LockReason             string
	HeadRevision           string
	PhysicalIdentityDigest string
	EvidenceDigest         string
	Reason                 string
}

type canonicalV19GitWorktreeRecord struct {
	Path           string
	HeadRevision   string
	LockReason     string
	Locked         bool
	Detached       bool
	Bare           bool
	Prunable       bool
	PrunableReason string
}

type canonicalV19WorktreeCreateNativeDeps struct {
	observe func(string, CanonicalV19WorktreeCreateRequest) canonicalV19GitWorktreeObservation
	perform func(string, CanonicalV19WorktreeCreateRequest) error
	now     func() time.Time
}

// ReconcileCanonicalV19WorktreeCreate observes one exact native Git create and
// performs it only when this call durably advances prepared to submitted.
func ReconcileCanonicalV19WorktreeCreate(ctx context.Context, homeDir, operationID string) (string, error) {
	return reconcileCanonicalV19WorktreeCreate(ctx, homeDir, operationID, canonicalV19WorktreeCreateNativeDeps{
		observe: observeCanonicalV19GitWorktree,
		perform: performCanonicalV19GitWorktreeCreate,
		now:     time.Now,
	})
}

func reconcileCanonicalV19WorktreeCreate(
	ctx context.Context,
	homeDir string,
	operationID string,
	deps canonicalV19WorktreeCreateNativeDeps,
) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if operationID == "" {
		return "", fmt.Errorf("reconcile canonical v19 WorktreeCreate: operation ID is empty")
	}
	if deps.observe == nil || deps.perform == nil || deps.now == nil {
		return "", fmt.Errorf("reconcile canonical v19 WorktreeCreate: native adapter dependencies are incomplete")
	}

	current, err := readCanonicalV19WorktreeCreateCurrent(ctx, homeDir, operationID)
	if err != nil {
		return "", fmt.Errorf("reconcile canonical v19 WorktreeCreate: %w", err)
	}
	switch current.State {
	case "succeeded", "rejected", "no-effect":
		return current.State, nil
	case "prepared", "submitted", "uncertain":
	default:
		return current.State, fmt.Errorf("reconcile canonical v19 WorktreeCreate: %w: operation %q is %q",
			ErrCanonicalV19WorktreeCreateTransition, operationID, current.State)
	}

	observed := deps.observe(homeDir, current.Request)
	if current.State != "prepared" {
		return reconcileCanonicalV19SubmittedWorktreeCreate(ctx, homeDir, current, observed, deps.now)
	}

	switch observed.State {
	case canonicalV19GitWorktreeExact:
		return establishCanonicalV19ObservedWorktree(ctx, homeDir, current, observed, deps.now)
	case canonicalV19GitWorktreeMismatch:
		return current.State, fmt.Errorf("reconcile canonical v19 WorktreeCreate: requested path ownership/registration is unresolved: %s", observed.Reason)
	case canonicalV19GitWorktreeUnknown:
		return current.State, fmt.Errorf("reconcile canonical v19 WorktreeCreate: native Git observation is unknown: %s", observed.Reason)
	case canonicalV19GitWorktreeAbsent:
	default:
		return current.State, fmt.Errorf("reconcile canonical v19 WorktreeCreate: native Git observation state %q is invalid", observed.State)
	}

	submittedAt := canonicalV19WorktreeCreateTimestampAfter(deps.now(), current.StateChangedAt)
	request, err := SubmitCanonicalV19WorktreeCreate(ctx, homeDir, operationID, submittedAt, observed.EvidenceDigest)
	if err != nil {
		return current.State, err
	}
	current.Request = request
	current.State = "submitted"
	current.StateChangedAt = submittedAt

	observed = deps.observe(homeDir, request)
	switch observed.State {
	case canonicalV19GitWorktreeExact:
		return establishCanonicalV19ObservedWorktree(ctx, homeDir, current, observed, deps.now)
	case canonicalV19GitWorktreeMismatch, canonicalV19GitWorktreeUnknown:
		return classifyCanonicalV19ObservedUncertain(ctx, homeDir, current, observed, deps.now, nil)
	case canonicalV19GitWorktreeAbsent:
	default:
		return current.State, fmt.Errorf("reconcile canonical v19 WorktreeCreate: native Git observation state %q is invalid", observed.State)
	}

	performErr := deps.perform(homeDir, request)
	observed = deps.observe(homeDir, request)
	switch observed.State {
	case canonicalV19GitWorktreeExact:
		return establishCanonicalV19ObservedWorktree(ctx, homeDir, current, observed, deps.now)
	case canonicalV19GitWorktreeAbsent:
		return classifyCanonicalV19ObservedNoEffect(ctx, homeDir, current, observed, deps.now,
			canonicalV19WorktreeCreatePerformFailure("native Git create left no registered worktree", performErr))
	case canonicalV19GitWorktreeMismatch, canonicalV19GitWorktreeUnknown:
		return classifyCanonicalV19ObservedUncertain(ctx, homeDir, current, observed, deps.now, performErr)
	default:
		return current.State, fmt.Errorf("reconcile canonical v19 WorktreeCreate: native Git observation state %q is invalid", observed.State)
	}
}

func reconcileCanonicalV19SubmittedWorktreeCreate(
	ctx context.Context,
	homeDir string,
	current canonicalV19WorktreeCreateCurrent,
	observed canonicalV19GitWorktreeObservation,
	now func() time.Time,
) (string, error) {
	switch observed.State {
	case canonicalV19GitWorktreeExact:
		return establishCanonicalV19ObservedWorktree(ctx, homeDir, current, observed, now)
	case canonicalV19GitWorktreeAbsent:
		return classifyCanonicalV19ObservedNoEffect(ctx, homeDir, current, observed, now,
			"positive observation proves the submitted create left no registered worktree")
	case canonicalV19GitWorktreeMismatch, canonicalV19GitWorktreeUnknown:
		if current.State == "submitted" {
			return classifyCanonicalV19ObservedUncertain(ctx, homeDir, current, observed, now, nil)
		}
		return "uncertain", fmt.Errorf("reconcile canonical v19 WorktreeCreate: operation remains uncertain: %s", observed.Reason)
	default:
		return current.State, fmt.Errorf("reconcile canonical v19 WorktreeCreate: native Git observation state %q is invalid", observed.State)
	}
}

func establishCanonicalV19ObservedWorktree(
	ctx context.Context,
	homeDir string,
	current canonicalV19WorktreeCreateCurrent,
	observed canonicalV19GitWorktreeObservation,
	now func() time.Time,
) (string, error) {
	request := current.Request
	establishedAt := canonicalV19WorktreeCreateTimestampAfter(now(), current.StateChangedAt)
	err := EstablishCanonicalV19WorktreeBinding(ctx, homeDir, CanonicalV19WorktreeBindingEvidence{
		OperationID:            request.OperationID,
		Path:                   request.RequestedPath,
		CommonGitDir:           request.ExpectedCommonGitDir,
		PrivateGitDir:          observed.PrivateGitDir,
		LockReason:             observed.LockReason,
		BasisRevision:          request.BasisRevision,
		HeadRevision:           observed.HeadRevision,
		PhysicalIdentityDigest: observed.PhysicalIdentityDigest,
		EstablishedAt:          establishedAt,
		EvidenceDigest:         observed.EvidenceDigest,
	})
	if err != nil {
		return current.State, err
	}
	return "succeeded", nil
}

func classifyCanonicalV19ObservedNoEffect(
	ctx context.Context,
	homeDir string,
	current canonicalV19WorktreeCreateCurrent,
	observed canonicalV19GitWorktreeObservation,
	now func() time.Time,
	reason string,
) (string, error) {
	observedAt := canonicalV19WorktreeCreateTimestampAfter(now(), current.StateChangedAt)
	if err := ClassifyCanonicalV19WorktreeCreate(ctx, homeDir, CanonicalV19WorktreeCreateTransitionInput{
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
	return "no-effect", fmt.Errorf("reconcile canonical v19 WorktreeCreate: %s", reason)
}

func classifyCanonicalV19ObservedUncertain(
	ctx context.Context,
	homeDir string,
	current canonicalV19WorktreeCreateCurrent,
	observed canonicalV19GitWorktreeObservation,
	now func() time.Time,
	performErr error,
) (string, error) {
	observedAt := canonicalV19WorktreeCreateTimestampAfter(now(), current.StateChangedAt)
	if err := ClassifyCanonicalV19WorktreeCreate(ctx, homeDir, CanonicalV19WorktreeCreateTransitionInput{
		OperationID:    current.Request.OperationID,
		State:          "uncertain",
		ObservedAt:     observedAt,
		EvidenceDigest: observed.EvidenceDigest,
	}); err != nil {
		return current.State, err
	}
	reason := observed.Reason
	if performErr != nil {
		reason = canonicalV19WorktreeCreatePerformFailure(reason, performErr)
	}
	return "uncertain", fmt.Errorf("reconcile canonical v19 WorktreeCreate: operation is uncertain: %s", reason)
}

func canonicalV19WorktreeCreatePerformFailure(prefix string, err error) string {
	if err == nil {
		return prefix
	}
	if prefix == "" {
		return fmt.Sprintf("native Git create failed: %v", err)
	}
	return fmt.Sprintf("%s; native Git create failed: %v", prefix, err)
}

func readCanonicalV19WorktreeCreateCurrent(ctx context.Context, homeDir, operationID string) (canonicalV19WorktreeCreateCurrent, error) {
	db, err := openReadOnly(homeDir)
	if err != nil {
		return canonicalV19WorktreeCreateCurrent{}, err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19WorktreeCreateCurrent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	return loadCanonicalV19WorktreeCreateCurrent(ctx, tx, operationID)
}

func observeCanonicalV19GitWorktree(homeDir string, request CanonicalV19WorktreeCreateRequest) canonicalV19GitWorktreeObservation {
	repositoryPath := canonicalV19ObservedPath(homeDir, request.RepositoryLocator)
	expectedCommonDir := canonicalV19ObservedPath(homeDir, request.ExpectedCommonGitDir)

	root, err := handgit.ResolveRoot(repositoryPath)
	if err != nil {
		return canonicalV19UnknownGitWorktree(request, fmt.Sprintf("resolve repository root: %v", err))
	}
	if !handgit.SamePath(root, repositoryPath) {
		return canonicalV19UnknownGitWorktree(request, "repository locator no longer resolves to its exact root")
	}
	commonDir, err := handgit.CommonDir(repositoryPath)
	if err != nil {
		return canonicalV19UnknownGitWorktree(request, fmt.Sprintf("resolve common Git directory: %v", err))
	}
	if !handgit.SamePath(commonDir, expectedCommonDir) {
		return canonicalV19UnknownGitWorktree(request, "WorkspaceBinding common Git directory no longer matches")
	}
	resolved, err := handgit.Run(repositoryPath, "rev-parse", "--verify", request.BasisRevision+"^{commit}")
	if err != nil || strings.TrimSpace(resolved) != request.BasisRevision {
		return canonicalV19UnknownGitWorktree(request, "exact basis revision is no longer positively resolvable")
	}

	listing, err := handgit.Run(repositoryPath, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return canonicalV19UnknownGitWorktree(request, fmt.Sprintf("list registered Git worktrees: %v", err))
	}
	records, err := parseCanonicalV19GitWorktreeList(listing)
	if err != nil {
		return canonicalV19UnknownGitWorktree(request, err.Error())
	}
	matches := make([]canonicalV19GitWorktreeRecord, 0, 1)
	for _, record := range records {
		if handgit.SamePath(filepath.FromSlash(record.Path), request.RequestedPath) {
			matches = append(matches, record)
		}
	}
	if len(matches) > 1 {
		return canonicalV19UnknownGitWorktree(request, "Git reports the requested path more than once")
	}
	if len(matches) == 0 {
		if _, err := os.Stat(request.RequestedPath); os.IsNotExist(err) {
			return canonicalV19FinalizeGitWorktreeObservation(request, canonicalV19GitWorktreeObservation{State: canonicalV19GitWorktreeAbsent})
		} else if err != nil {
			return canonicalV19UnknownGitWorktree(request, fmt.Sprintf("inspect requested path: %v", err))
		}
		return canonicalV19FinalizeGitWorktreeObservation(request, canonicalV19GitWorktreeObservation{
			State:  canonicalV19GitWorktreeMismatch,
			Reason: "requested path exists but is not registered in the expected repository",
		})
	}

	record := matches[0]
	if record.Bare || record.Prunable || !record.Detached || !record.Locked || record.LockReason != request.ExpectedLockReason || record.HeadRevision != request.BasisRevision {
		return canonicalV19FinalizeGitWorktreeObservation(request, canonicalV19GitWorktreeObservation{
			State:        canonicalV19GitWorktreeMismatch,
			LockReason:   record.LockReason,
			HeadRevision: record.HeadRevision,
			Reason:       "registered worktree metadata does not match the exact detached, locked request",
		})
	}
	info, err := os.Stat(request.RequestedPath)
	if err != nil {
		return canonicalV19FinalizeGitWorktreeObservation(request, canonicalV19GitWorktreeObservation{
			State:  canonicalV19GitWorktreeMismatch,
			Reason: fmt.Sprintf("registered worktree path cannot be inspected: %v", err),
		})
	}
	if !info.IsDir() {
		return canonicalV19FinalizeGitWorktreeObservation(request, canonicalV19GitWorktreeObservation{
			State:  canonicalV19GitWorktreeMismatch,
			Reason: "registered worktree path is not a directory",
		})
	}
	worktreeRoot, err := handgit.ResolveRoot(request.RequestedPath)
	if err != nil || !handgit.SamePath(worktreeRoot, request.RequestedPath) {
		return canonicalV19FinalizeGitWorktreeObservation(request, canonicalV19GitWorktreeObservation{
			State:  canonicalV19GitWorktreeMismatch,
			Reason: "registered worktree does not resolve to the requested repository root",
		})
	}
	worktreeCommonDir, err := handgit.CommonDir(request.RequestedPath)
	if err != nil || !handgit.SamePath(worktreeCommonDir, expectedCommonDir) {
		return canonicalV19FinalizeGitWorktreeObservation(request, canonicalV19GitWorktreeObservation{
			State:  canonicalV19GitWorktreeMismatch,
			Reason: "registered worktree common Git directory does not match",
		})
	}
	privateGitDirOutput, err := handgit.Run(request.RequestedPath, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return canonicalV19UnknownGitWorktree(request, fmt.Sprintf("resolve private Git directory: %v", err))
	}
	privateGitDir := filepath.Clean(filepath.FromSlash(strings.TrimSpace(privateGitDirOutput)))
	if privateGitDir == "." || privateGitDir == "" {
		return canonicalV19UnknownGitWorktree(request, "private Git directory is empty")
	}
	headRevision, err := handgit.HeadCommit(request.RequestedPath)
	if err != nil || headRevision != request.BasisRevision {
		return canonicalV19FinalizeGitWorktreeObservation(request, canonicalV19GitWorktreeObservation{
			State:         canonicalV19GitWorktreeMismatch,
			PrivateGitDir: privateGitDir,
			LockReason:    record.LockReason,
			HeadRevision:  headRevision,
			Reason:        "worktree HEAD does not match the exact basis revision",
		})
	}
	finalInfo, err := os.Stat(request.RequestedPath)
	if err != nil || !os.SameFile(info, finalInfo) {
		return canonicalV19UnknownGitWorktree(request, "worktree physical identity changed during observation")
	}
	physicalIdentity, err := canonicalV19WorktreePhysicalIdentity(request.RequestedPath, finalInfo)
	if err != nil {
		return canonicalV19UnknownGitWorktree(request, fmt.Sprintf("capture worktree physical identity: %v", err))
	}

	return canonicalV19FinalizeGitWorktreeObservation(request, canonicalV19GitWorktreeObservation{
		State:                  canonicalV19GitWorktreeExact,
		PrivateGitDir:          privateGitDir,
		LockReason:             record.LockReason,
		HeadRevision:           headRevision,
		PhysicalIdentityDigest: canonicalV19WorktreePhysicalIdentityDigest(physicalIdentity),
	})
}

func performCanonicalV19GitWorktreeCreate(homeDir string, request CanonicalV19WorktreeCreateRequest) error {
	repositoryPath := canonicalV19ObservedPath(homeDir, request.RepositoryLocator)
	_, err := handgit.Run(repositoryPath, "worktree", "add", "--detach", "--lock", "--reason", request.ExpectedLockReason,
		request.RequestedPath, request.BasisRevision)
	if err != nil {
		return fmt.Errorf("git worktree add: %w", err)
	}
	return nil
}

func parseCanonicalV19GitWorktreeList(output string) ([]canonicalV19GitWorktreeRecord, error) {
	records := make([]canonicalV19GitWorktreeRecord, 0)
	var current canonicalV19GitWorktreeRecord
	flush := func() error {
		if current.Path == "" {
			return nil
		}
		records = append(records, current)
		current = canonicalV19GitWorktreeRecord{}
		return nil
	}
	for _, field := range strings.Split(output, "\x00") {
		if field == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		name, value, _ := strings.Cut(field, " ")
		switch name {
		case "worktree":
			if current.Path != "" {
				return nil, fmt.Errorf("parse Git worktree list: record boundary is missing before %q", value)
			}
			if value == "" {
				return nil, fmt.Errorf("parse Git worktree list: empty worktree path")
			}
			current.Path = value
		case "HEAD":
			current.HeadRevision = value
		case "locked":
			current.Locked = true
			current.LockReason = value
		case "detached":
			current.Detached = true
		case "bare":
			current.Bare = true
		case "prunable":
			current.Prunable = true
			current.PrunableReason = value
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return records, nil
}

func canonicalV19UnknownGitWorktree(request CanonicalV19WorktreeCreateRequest, reason string) canonicalV19GitWorktreeObservation {
	return canonicalV19FinalizeGitWorktreeObservation(request, canonicalV19GitWorktreeObservation{
		State:  canonicalV19GitWorktreeUnknown,
		Reason: reason,
	})
}

func canonicalV19FinalizeGitWorktreeObservation(
	request CanonicalV19WorktreeCreateRequest,
	observed canonicalV19GitWorktreeObservation,
) canonicalV19GitWorktreeObservation {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:git-worktree-create-observation:v1")
	writeCanonicalV19DigestField(hash, "request_digest", request.RequestDigest)
	writeCanonicalV19DigestField(hash, "state", string(observed.State))
	writeCanonicalV19DigestField(hash, "private_git_dir", observed.PrivateGitDir)
	writeCanonicalV19DigestField(hash, "lock_reason", observed.LockReason)
	writeCanonicalV19DigestField(hash, "head_revision", observed.HeadRevision)
	writeCanonicalV19DigestField(hash, "physical_identity_digest", observed.PhysicalIdentityDigest)
	writeCanonicalV19DigestField(hash, "reason", observed.Reason)
	observed.EvidenceDigest = hex.EncodeToString(hash.Sum(nil))
	return observed
}

func canonicalV19WorktreePhysicalIdentityDigest(identity string) string {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:worktree-physical-identity:v1")
	writeCanonicalV19DigestField(hash, "identity", identity)
	return hex.EncodeToString(hash.Sum(nil))
}

func canonicalV19WorktreeCreateTimestampAfter(now time.Time, previous string) string {
	candidate := now.UTC()
	if prior, err := time.Parse(time.RFC3339Nano, previous); err == nil && !candidate.After(prior) {
		candidate = prior.Add(time.Nanosecond)
	}
	return candidate.Format(time.RFC3339Nano)
}
