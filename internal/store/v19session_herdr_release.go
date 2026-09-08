package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	handgit "github.com/atqamz/hand/internal/git"
	"github.com/atqamz/hand/internal/herdr"
)

type canonicalV19HerdrSessionReleaseCurrent struct {
	Current      canonicalV19SessionReleaseCurrent
	FleetID      string
	WorktreePath string
}

type canonicalV19HerdrSessionReleaseClient interface {
	ObserveSession(context.Context) herdr.SessionObservation
	WorkspaceListContext(context.Context) ([]herdr.Workspace, error)
	WorkspaceClose(string) error
	TabList(string) ([]herdr.Tab, error)
	PaneGetContext(context.Context, string) (herdr.Pane, error)
}

type canonicalV19HerdrSessionReleaseDeps struct {
	clientFor func(string) canonicalV19HerdrSessionReleaseClient
	now       func() time.Time
}

// ReconcileCanonicalV19HerdrSessionRelease reconciles one exact canonical v19 SessionRelease against Herdr.
// It durably submits before workspace close and never blindly retries a submitted or uncertain provider mutation.
// Success requires positive absence of the exact persisted workspace/pane addressability.
func ReconcileCanonicalV19HerdrSessionRelease(ctx context.Context, homeDir, operationID string) (string, error) {
	return reconcileCanonicalV19HerdrSessionRelease(ctx, homeDir, operationID, canonicalV19HerdrSessionReleaseDeps{
		clientFor: func(sessionName string) canonicalV19HerdrSessionReleaseClient {
			return herdr.NewManagedSessionClient(sessionName)
		},
		now: time.Now,
	})
}

func reconcileCanonicalV19HerdrSessionRelease(
	ctx context.Context,
	homeDir string,
	operationID string,
	deps canonicalV19HerdrSessionReleaseDeps,
) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if operationID == "" {
		return "", fmt.Errorf("reconcile canonical v19 Herdr SessionRelease: operation ID is empty")
	}
	if deps.clientFor == nil || deps.now == nil {
		return "", fmt.Errorf("reconcile canonical v19 Herdr SessionRelease: adapter dependencies are incomplete")
	}

	current, err := readCanonicalV19HerdrSessionReleaseCurrent(ctx, homeDir, operationID)
	if err != nil {
		if errors.Is(err, ErrCanonicalV19SessionNotCurrent) {
			if state, found, terminalErr := readCanonicalV19HerdrSessionReleaseTerminal(ctx, homeDir, operationID); terminalErr != nil {
				return "", fmt.Errorf("reconcile canonical v19 Herdr SessionRelease: %w", terminalErr)
			} else if found {
				return state, nil
			}
		}
		return "", fmt.Errorf("reconcile canonical v19 Herdr SessionRelease: %w", err)
	}
	request := current.Current.Request
	switch current.Current.State {
	case "succeeded", "rejected", "no-effect":
		return current.Current.State, nil
	case "prepared", "submitted", "uncertain":
	default:
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr SessionRelease: %w: operation %q is %q",
			ErrCanonicalV19SessionTransition, operationID, current.Current.State)
	}
	if request.AdapterRef != canonicalV19HerdrSessionAdapterRef {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr SessionRelease: %w: adapter %q is not %q",
			ErrCanonicalV19SessionNotCurrent, request.AdapterRef, canonicalV19HerdrSessionAdapterRef)
	}
	key, err := parseCanonicalV19HerdrSessionProviderKey(request.ExpectedProviderSessionKey)
	if err != nil {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr SessionRelease: %w: invalid provider Session key: %v",
			ErrCanonicalV19SessionNotCurrent, err)
	}
	expectedSession := herdr.SessionName(current.FleetID)
	if key.SessionName != expectedSession {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr SessionRelease: %w: provider Session key names %q, want %q",
			ErrCanonicalV19SessionNotCurrent, key.SessionName, expectedSession)
	}
	client := deps.clientFor(expectedSession)
	if client == nil {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr SessionRelease: provider client is unavailable")
	}

	observed := observeCanonicalV19HerdrSessionRelease(ctx, current, client)
	if current.Current.State != "prepared" {
		return reconcileCanonicalV19SubmittedHerdrSessionRelease(ctx, homeDir, current, observed, deps.now)
	}
	switch observed.State {
	case canonicalV19HerdrSessionAbsent:
		return completeCanonicalV19ObservedHerdrSessionRelease(ctx, homeDir, current, observed, deps.now)
	case canonicalV19HerdrSessionExact:
	case canonicalV19HerdrSessionMismatch:
		return "prepared", fmt.Errorf("reconcile canonical v19 Herdr SessionRelease: exact provider ownership is unresolved: %s", observed.Reason)
	case canonicalV19HerdrSessionUnknown:
		return "prepared", fmt.Errorf("reconcile canonical v19 Herdr SessionRelease: provider observation is unknown: %s", observed.Reason)
	default:
		return "prepared", fmt.Errorf("reconcile canonical v19 Herdr SessionRelease: provider observation state %q is invalid", observed.State)
	}

	submittedAt := canonicalV19HerdrSessionTimestampAfter(deps.now(), current.Current.StateChangedAt)
	submitted, err := SubmitCanonicalV19SessionRelease(ctx, homeDir, operationID, submittedAt, observed.EvidenceDigest)
	if err != nil {
		return "prepared", err
	}
	current.Current.Request = submitted
	current.Current.State = "submitted"
	current.Current.StateChangedAt = submittedAt

	observed = observeCanonicalV19HerdrSessionRelease(ctx, current, client)
	switch observed.State {
	case canonicalV19HerdrSessionAbsent:
		return completeCanonicalV19ObservedHerdrSessionRelease(ctx, homeDir, current, observed, deps.now)
	case canonicalV19HerdrSessionExact:
	case canonicalV19HerdrSessionMismatch, canonicalV19HerdrSessionUnknown:
		return classifyCanonicalV19HerdrSessionReleaseUncertain(ctx, homeDir, current, observed, deps.now, nil)
	default:
		return "submitted", fmt.Errorf("reconcile canonical v19 Herdr SessionRelease: provider observation state %q is invalid", observed.State)
	}

	performErr := client.WorkspaceClose(key.WorkspaceID)
	observed = observeCanonicalV19HerdrSessionRelease(ctx, current, client)
	switch observed.State {
	case canonicalV19HerdrSessionAbsent:
		return completeCanonicalV19ObservedHerdrSessionRelease(ctx, homeDir, current, observed, deps.now)
	case canonicalV19HerdrSessionExact:
		if performErr != nil && herdr.IsProcessNotStarted(performErr) {
			return classifyCanonicalV19HerdrSessionReleaseNoEffect(ctx, homeDir, current, observed, deps.now,
				"Herdr process did not start and exact provider Session addressability remains unchanged")
		}
		return classifyCanonicalV19HerdrSessionReleaseUncertain(ctx, homeDir, current, observed, deps.now, performErr)
	case canonicalV19HerdrSessionMismatch, canonicalV19HerdrSessionUnknown:
		return classifyCanonicalV19HerdrSessionReleaseUncertain(ctx, homeDir, current, observed, deps.now, performErr)
	default:
		return "submitted", fmt.Errorf("reconcile canonical v19 Herdr SessionRelease: provider observation state %q is invalid", observed.State)
	}
}

func reconcileCanonicalV19SubmittedHerdrSessionRelease(
	ctx context.Context,
	homeDir string,
	current canonicalV19HerdrSessionReleaseCurrent,
	observed canonicalV19HerdrSessionObservation,
	now func() time.Time,
) (string, error) {
	switch observed.State {
	case canonicalV19HerdrSessionAbsent:
		return completeCanonicalV19ObservedHerdrSessionRelease(ctx, homeDir, current, observed, now)
	case canonicalV19HerdrSessionExact, canonicalV19HerdrSessionMismatch, canonicalV19HerdrSessionUnknown:
		if observed.Reason == "" {
			observed.Reason = "submitted SessionRelease cannot be replayed without positive absence evidence"
		} else {
			observed.Reason += "; submitted SessionRelease cannot be replayed without positive absence evidence"
		}
		observed = finalizeCanonicalV19HerdrSessionReleaseObservation(current, observed)
		if current.Current.State == "submitted" {
			return classifyCanonicalV19HerdrSessionReleaseUncertain(ctx, homeDir, current, observed, now, nil)
		}
		return "uncertain", fmt.Errorf("reconcile canonical v19 Herdr SessionRelease: operation remains uncertain: %s", observed.Reason)
	default:
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr SessionRelease: provider observation state %q is invalid", observed.State)
	}
}

func completeCanonicalV19ObservedHerdrSessionRelease(
	ctx context.Context,
	homeDir string,
	current canonicalV19HerdrSessionReleaseCurrent,
	observed canonicalV19HerdrSessionObservation,
	now func() time.Time,
) (string, error) {
	releasedAt := canonicalV19HerdrSessionTimestampAfter(now(), current.Current.StateChangedAt)
	if err := CompleteCanonicalV19SessionRelease(ctx, homeDir, CanonicalV19SessionReleasedEvidence{
		OperationID:    current.Current.Request.OperationID,
		ReleasedAt:     releasedAt,
		EvidenceDigest: observed.EvidenceDigest,
	}); err != nil {
		return current.Current.State, err
	}
	return "succeeded", nil
}

func classifyCanonicalV19HerdrSessionReleaseNoEffect(
	ctx context.Context,
	homeDir string,
	current canonicalV19HerdrSessionReleaseCurrent,
	observed canonicalV19HerdrSessionObservation,
	now func() time.Time,
	reason string,
) (string, error) {
	observedAt := canonicalV19HerdrSessionTimestampAfter(now(), current.Current.StateChangedAt)
	if err := ClassifyCanonicalV19SessionRelease(ctx, homeDir, CanonicalV19SessionReleaseTransitionInput{
		OperationID:    current.Current.Request.OperationID,
		State:          "no-effect",
		ObservedAt:     observedAt,
		EvidenceDigest: observed.EvidenceDigest,
	}); err != nil {
		return current.Current.State, err
	}
	if observed.Reason != "" {
		reason += ": " + observed.Reason
	}
	return "no-effect", fmt.Errorf("reconcile canonical v19 Herdr SessionRelease: %s", reason)
}

func classifyCanonicalV19HerdrSessionReleaseUncertain(
	ctx context.Context,
	homeDir string,
	current canonicalV19HerdrSessionReleaseCurrent,
	observed canonicalV19HerdrSessionObservation,
	now func() time.Time,
	performErr error,
) (string, error) {
	if current.Current.State == "uncertain" {
		return "uncertain", fmt.Errorf("reconcile canonical v19 Herdr SessionRelease: operation remains uncertain: %s", observed.Reason)
	}
	observedAt := canonicalV19HerdrSessionTimestampAfter(now(), current.Current.StateChangedAt)
	if err := ClassifyCanonicalV19SessionRelease(ctx, homeDir, CanonicalV19SessionReleaseTransitionInput{
		OperationID:    current.Current.Request.OperationID,
		State:          "uncertain",
		ObservedAt:     observedAt,
		EvidenceDigest: observed.EvidenceDigest,
	}); err != nil {
		return current.Current.State, err
	}
	reason := observed.Reason
	if performErr != nil {
		reason = canonicalV19HerdrSessionReleasePerformFailure(reason, performErr)
	}
	if reason == "" {
		reason = "strongest provider evidence cannot classify the submitted release"
	}
	return "uncertain", fmt.Errorf("reconcile canonical v19 Herdr SessionRelease: operation is uncertain: %s", reason)
}

func observeCanonicalV19HerdrSessionRelease(
	ctx context.Context,
	current canonicalV19HerdrSessionReleaseCurrent,
	client canonicalV19HerdrSessionReleaseClient,
) canonicalV19HerdrSessionObservation {
	request := current.Current.Request
	providerKey := request.ExpectedProviderSessionKey
	key, err := parseCanonicalV19HerdrSessionProviderKey(providerKey)
	if err != nil {
		return finalizeCanonicalV19HerdrSessionReleaseObservation(current, canonicalV19HerdrSessionObservation{
			State: canonicalV19HerdrSessionMismatch, ProviderSessionKey: providerKey,
			Reason: "provider Session key is invalid: " + err.Error(),
		})
	}
	expectedSession := herdr.SessionName(current.FleetID)
	observation := canonicalV19HerdrSessionObservation{
		ProviderSessionKey: providerKey,
		WorkspaceID:        key.WorkspaceID,
		TabID:              key.TabID,
		PaneID:             key.PaneID,
	}
	if key.SessionName != expectedSession {
		observation.State = canonicalV19HerdrSessionMismatch
		observation.Reason = fmt.Sprintf("provider Session key names Herdr session %q, want %q", key.SessionName, expectedSession)
		return finalizeCanonicalV19HerdrSessionReleaseObservation(current, observation)
	}
	session := client.ObserveSession(ctx)
	if session.Name != expectedSession || session.State != herdr.SessionRunningCompatible {
		observation.State = canonicalV19HerdrSessionUnknown
		observation.Reason = fmt.Sprintf("exact Herdr session %q is %q", expectedSession, session.State)
		if session.Reason != "" {
			observation.Reason += ": " + session.Reason
		}
		return finalizeCanonicalV19HerdrSessionReleaseObservation(current, observation)
	}

	workspaces, err := client.WorkspaceListContext(ctx)
	if err != nil {
		observation.State = canonicalV19HerdrSessionUnknown
		observation.Reason = "list exact Herdr session workspaces: " + canonicalV19HerdrSessionErrorText(err)
		return finalizeCanonicalV19HerdrSessionReleaseObservation(current, observation)
	}
	var workspace herdr.Workspace
	workspaceMatches := 0
	for _, candidate := range workspaces {
		if candidate.WorkspaceID == key.WorkspaceID {
			workspace = candidate
			workspaceMatches++
		}
	}
	if workspaceMatches == 0 {
		pane, paneErr := client.PaneGetContext(ctx, key.PaneID)
		if errors.Is(paneErr, herdr.ErrNotFound) {
			observation.State = canonicalV19HerdrSessionAbsent
			return finalizeCanonicalV19HerdrSessionReleaseObservation(current, observation)
		}
		if paneErr != nil {
			observation.State = canonicalV19HerdrSessionUnknown
			observation.Reason = "workspace absent but exact pane could not be disproven: " + canonicalV19HerdrSessionErrorText(paneErr)
			return finalizeCanonicalV19HerdrSessionReleaseObservation(current, observation)
		}
		observation.State = canonicalV19HerdrSessionMismatch
		observation.PaneCwd = pane.Cwd
		observation.Reason = "exact pane remains live while its provider Session key workspace is absent from inventory"
		return finalizeCanonicalV19HerdrSessionReleaseObservation(current, observation)
	}
	if workspaceMatches != 1 {
		observation.State = canonicalV19HerdrSessionUnknown
		observation.Reason = "Herdr workspace inventory returned the exact workspace identity more than once"
		return finalizeCanonicalV19HerdrSessionReleaseObservation(current, observation)
	}
	if workspace.Label != canonicalV19HerdrSessionWorkspaceLabel(request.SessionBindingID) {
		observation.State = canonicalV19HerdrSessionMismatch
		observation.Reason = "exact workspace identity no longer carries the dedicated Session locator"
		return finalizeCanonicalV19HerdrSessionReleaseObservation(current, observation)
	}

	tabs, err := client.TabList(key.WorkspaceID)
	if err != nil {
		observation.State = canonicalV19HerdrSessionUnknown
		observation.Reason = "list exact Herdr workspace tabs: " + canonicalV19HerdrSessionErrorText(err)
		return finalizeCanonicalV19HerdrSessionReleaseObservation(current, observation)
	}
	if len(tabs) != 1 {
		observation.State = canonicalV19HerdrSessionMismatch
		observation.Reason = fmt.Sprintf("dedicated Session workspace has %d tabs, want exactly 1", len(tabs))
		return finalizeCanonicalV19HerdrSessionReleaseObservation(current, observation)
	}
	if tabs[0].TabID != key.TabID || tabs[0].WorkspaceID != key.WorkspaceID {
		observation.State = canonicalV19HerdrSessionMismatch
		observation.Reason = "dedicated Session workspace root tab identity differs from the provider Session key"
		return finalizeCanonicalV19HerdrSessionReleaseObservation(current, observation)
	}

	pane, err := client.PaneGetContext(ctx, key.PaneID)
	if errors.Is(err, herdr.ErrNotFound) {
		observation.State = canonicalV19HerdrSessionMismatch
		observation.Reason = "provider Session key workspace/tab remain but the exact root pane is absent"
		return finalizeCanonicalV19HerdrSessionReleaseObservation(current, observation)
	}
	if err != nil {
		observation.State = canonicalV19HerdrSessionUnknown
		observation.Reason = "observe exact Herdr root pane: " + canonicalV19HerdrSessionErrorText(err)
		return finalizeCanonicalV19HerdrSessionReleaseObservation(current, observation)
	}
	observation.PaneCwd = pane.Cwd
	if pane.PaneID != key.PaneID || pane.TabID != key.TabID || pane.WorkspaceID != key.WorkspaceID {
		observation.State = canonicalV19HerdrSessionMismatch
		observation.Reason = "exact root pane parent identities differ from the provider Session key"
		return finalizeCanonicalV19HerdrSessionReleaseObservation(current, observation)
	}
	if pane.Cwd == "" || !handgit.SamePath(pane.Cwd, current.WorktreePath) {
		observation.State = canonicalV19HerdrSessionMismatch
		observation.Reason = "exact root pane cwd differs from the immutable WorktreeBinding path"
		return finalizeCanonicalV19HerdrSessionReleaseObservation(current, observation)
	}
	observation.State = canonicalV19HerdrSessionExact
	return finalizeCanonicalV19HerdrSessionReleaseObservation(current, observation)
}

func readCanonicalV19HerdrSessionReleaseCurrent(
	ctx context.Context,
	homeDir string,
	operationID string,
) (canonicalV19HerdrSessionReleaseCurrent, error) {
	db, err := openReadOnly(homeDir)
	if err != nil {
		return canonicalV19HerdrSessionReleaseCurrent{}, err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19HerdrSessionReleaseCurrent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := loadCanonicalV19SessionReleaseCurrent(ctx, tx, operationID)
	if err != nil {
		return canonicalV19HerdrSessionReleaseCurrent{}, err
	}
	result := canonicalV19HerdrSessionReleaseCurrent{Current: current}
	if err := tx.QueryRowContext(ctx, `SELECT p.fleet_id,b.path
		FROM project p
		JOIN attempt_worktree_binding b ON b.id=? AND b.attempt_id=?
		WHERE p.id=?`, current.Request.WorktreeBindingID, current.Request.AttemptID, current.Request.ProjectID).Scan(
		&result.FleetID, &result.WorktreePath,
	); err != nil {
		return canonicalV19HerdrSessionReleaseCurrent{}, fmt.Errorf("read canonical v19 Herdr SessionRelease provider context: %w", err)
	}
	if result.FleetID == "" || result.WorktreePath == "" {
		return canonicalV19HerdrSessionReleaseCurrent{}, fmt.Errorf("read canonical v19 Herdr SessionRelease provider context: Fleet ID or WorktreeBinding path is empty")
	}
	return result, nil
}

func readCanonicalV19HerdrSessionReleaseTerminal(ctx context.Context, homeDir, operationID string) (string, bool, error) {
	db, err := openReadOnly(homeDir)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = db.Close() }()
	var state string
	err = db.sql.QueryRowContext(ctx, `SELECT o.state
		FROM external_operation o
		JOIN session_release_operation sr ON sr.operation_id=o.id
		JOIN session_binding_release r ON r.release_operation_id=o.id AND r.session_binding_id=sr.session_binding_id
		WHERE o.id=? AND o.kind='session-release' AND o.state='succeeded'`, operationID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return state, true, nil
}

func finalizeCanonicalV19HerdrSessionReleaseObservation(
	current canonicalV19HerdrSessionReleaseCurrent,
	observed canonicalV19HerdrSessionObservation,
) canonicalV19HerdrSessionObservation {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:herdr-session-release-observation:v1")
	writeCanonicalV19DigestField(hash, "operation_id", current.Current.Request.OperationID)
	writeCanonicalV19DigestField(hash, "request_digest", current.Current.Request.RequestDigest)
	writeCanonicalV19DigestField(hash, "fleet_id", current.FleetID)
	writeCanonicalV19DigestField(hash, "worktree_path", current.WorktreePath)
	writeCanonicalV19DigestField(hash, "workspace_label", canonicalV19HerdrSessionWorkspaceLabel(current.Current.Request.SessionBindingID))
	writeCanonicalV19DigestField(hash, "state", string(observed.State))
	writeCanonicalV19DigestField(hash, "provider_session_key", observed.ProviderSessionKey)
	writeCanonicalV19DigestField(hash, "workspace_id", observed.WorkspaceID)
	writeCanonicalV19DigestField(hash, "tab_id", observed.TabID)
	writeCanonicalV19DigestField(hash, "pane_id", observed.PaneID)
	writeCanonicalV19DigestField(hash, "pane_cwd", observed.PaneCwd)
	writeCanonicalV19DigestField(hash, "reason", observed.Reason)
	observed.EvidenceDigest = hex.EncodeToString(hash.Sum(nil))
	return observed
}

func canonicalV19HerdrSessionReleasePerformFailure(prefix string, err error) string {
	if err == nil {
		return prefix
	}
	failure := "Herdr Session release failed: " + canonicalV19HerdrSessionErrorText(err)
	if prefix == "" {
		return failure
	}
	return prefix + "; " + failure
}
