package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	handgit "github.com/atqamz/hand/internal/git"
	"github.com/atqamz/hand/internal/herdr"
)

const canonicalV19HerdrSessionAdapterRef = "herdr"

type canonicalV19HerdrSessionObservationState string

const (
	canonicalV19HerdrSessionExact    canonicalV19HerdrSessionObservationState = "exact"
	canonicalV19HerdrSessionAbsent   canonicalV19HerdrSessionObservationState = "absent"
	canonicalV19HerdrSessionMismatch canonicalV19HerdrSessionObservationState = "mismatch"
	canonicalV19HerdrSessionUnknown  canonicalV19HerdrSessionObservationState = "unknown"
)

type canonicalV19HerdrSessionProviderKey struct {
	SessionName string
	WorkspaceID string
	TabID       string
	PaneID      string
}

type canonicalV19HerdrSessionObservation struct {
	State              canonicalV19HerdrSessionObservationState
	ProviderSessionKey string
	WorkspaceID        string
	TabID              string
	PaneID             string
	PaneCwd            string
	LabelMatches       int
	EvidenceDigest     string
	Reason             string
}

type canonicalV19HerdrSessionAcquireCurrent struct {
	Current      canonicalV19SessionAcquireCurrent
	FleetID      string
	WorktreePath string
}

type canonicalV19HerdrSessionClient interface {
	ObserveSession(context.Context) herdr.SessionObservation
	WorkspaceListContext(context.Context) ([]herdr.Workspace, error)
	WorkspaceCreate(string, map[string]string, string) (herdr.Workspace, herdr.Tab, herdr.Pane, error)
	TabList(string) ([]herdr.Tab, error)
	PaneGetContext(context.Context, string) (herdr.Pane, error)
}

type canonicalV19HerdrSessionAcquireDeps struct {
	clientFor func(string) canonicalV19HerdrSessionClient
	now       func() time.Time
}

// ReconcileCanonicalV19HerdrSessionAcquire reconciles one exact canonical v19 SessionAcquire against Herdr.
// Fresh acquisition requires an empty RequestedProviderSessionKey because Herdr assigns provider identities.
// Recovery never blindly retries or adopts a workspace from its locator label after those identities are lost.
func ReconcileCanonicalV19HerdrSessionAcquire(ctx context.Context, homeDir, operationID string) (string, error) {
	return reconcileCanonicalV19HerdrSessionAcquire(ctx, homeDir, operationID, canonicalV19HerdrSessionAcquireDeps{
		clientFor: func(sessionName string) canonicalV19HerdrSessionClient {
			return herdr.NewManagedSessionClient(sessionName)
		},
		now: time.Now,
	})
}

func reconcileCanonicalV19HerdrSessionAcquire(
	ctx context.Context,
	homeDir string,
	operationID string,
	deps canonicalV19HerdrSessionAcquireDeps,
) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if operationID == "" {
		return "", fmt.Errorf("reconcile canonical v19 Herdr SessionAcquire: operation ID is empty")
	}
	if deps.clientFor == nil || deps.now == nil {
		return "", fmt.Errorf("reconcile canonical v19 Herdr SessionAcquire: adapter dependencies are incomplete")
	}

	current, err := readCanonicalV19HerdrSessionAcquireCurrent(ctx, homeDir, operationID)
	if err != nil {
		return "", fmt.Errorf("reconcile canonical v19 Herdr SessionAcquire: %w", err)
	}
	request := current.Current.Request
	switch current.Current.State {
	case "succeeded", "rejected", "no-effect":
		return current.Current.State, nil
	case "prepared", "submitted", "uncertain":
	default:
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr SessionAcquire: %w: operation %q is %q",
			ErrCanonicalV19SessionTransition, operationID, current.Current.State)
	}
	if request.AdapterRef != canonicalV19HerdrSessionAdapterRef {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr SessionAcquire: %w: adapter %q is not %q",
			ErrCanonicalV19SessionNotCurrent, request.AdapterRef, canonicalV19HerdrSessionAdapterRef)
	}
	if request.RequestedProviderSessionKey != "" {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr SessionAcquire: provider-assigned Session key must be empty before fresh acquisition")
	}

	client := deps.clientFor(herdr.SessionName(current.FleetID))
	if client == nil {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr SessionAcquire: provider client is unavailable")
	}
	observed := observeCanonicalV19HerdrSessionAcquire(ctx, current, "", client)
	if current.Current.State != "prepared" {
		return reconcileCanonicalV19SubmittedHerdrSessionAcquire(ctx, homeDir, current, observed, deps.now)
	}

	switch observed.State {
	case canonicalV19HerdrSessionAbsent:
	case canonicalV19HerdrSessionMismatch:
		return "prepared", fmt.Errorf("reconcile canonical v19 Herdr SessionAcquire: provider resource ownership is unresolved: %s", observed.Reason)
	case canonicalV19HerdrSessionUnknown:
		return "prepared", fmt.Errorf("reconcile canonical v19 Herdr SessionAcquire: provider observation is unknown: %s", observed.Reason)
	default:
		return "prepared", fmt.Errorf("reconcile canonical v19 Herdr SessionAcquire: provider observation state %q is invalid", observed.State)
	}

	submittedAt := canonicalV19HerdrSessionTimestampAfter(deps.now(), current.Current.StateChangedAt)
	submitted, err := SubmitCanonicalV19SessionAcquire(ctx, homeDir, operationID, submittedAt, observed.EvidenceDigest)
	if err != nil {
		return "prepared", err
	}
	current.Current.Request = submitted
	current.Current.State = "submitted"
	current.Current.StateChangedAt = submittedAt

	observed = observeCanonicalV19HerdrSessionAcquire(ctx, current, "", client)
	if observed.State != canonicalV19HerdrSessionAbsent {
		return classifyCanonicalV19HerdrSessionAcquireUncertain(ctx, homeDir, current, observed, deps.now, nil)
	}

	providerKey, performErr := performCanonicalV19HerdrSessionAcquire(current, client)
	if providerKey != "" {
		observed = observeCanonicalV19HerdrSessionAcquire(ctx, current, providerKey, client)
		switch observed.State {
		case canonicalV19HerdrSessionExact:
			return establishCanonicalV19ObservedHerdrSession(ctx, homeDir, current, observed, deps.now)
		case canonicalV19HerdrSessionAbsent:
			return classifyCanonicalV19HerdrSessionAcquireNoEffect(ctx, homeDir, current, observed, deps.now,
				"positive observation proves the exact provider-assigned Session resource is absent")
		case canonicalV19HerdrSessionMismatch, canonicalV19HerdrSessionUnknown:
			return classifyCanonicalV19HerdrSessionAcquireUncertain(ctx, homeDir, current, observed, deps.now, performErr)
		default:
			return "submitted", fmt.Errorf("reconcile canonical v19 Herdr SessionAcquire: provider observation state %q is invalid", observed.State)
		}
	}

	observed = observeCanonicalV19HerdrSessionAcquire(ctx, current, "", client)
	if performErr != nil && herdr.IsProcessNotStarted(performErr) && observed.State == canonicalV19HerdrSessionAbsent {
		return classifyCanonicalV19HerdrSessionAcquireNoEffect(ctx, homeDir, current, observed, deps.now,
			"Herdr process did not start and positive pre/post observation proves no dedicated Session locator exists")
	}
	return classifyCanonicalV19HerdrSessionAcquireUncertain(ctx, homeDir, current, observed, deps.now, performErr)
}

func reconcileCanonicalV19SubmittedHerdrSessionAcquire(
	ctx context.Context,
	homeDir string,
	current canonicalV19HerdrSessionAcquireCurrent,
	observed canonicalV19HerdrSessionObservation,
	now func() time.Time,
) (string, error) {
	if observed.Reason == "" {
		observed.Reason = "provider-assigned workspace/tab/pane identity was not durably captured before recovery"
	} else {
		observed.Reason += "; provider-assigned workspace/tab/pane identity was not durably captured before recovery"
	}
	observed = finalizeCanonicalV19HerdrSessionObservation(current, observed)
	if current.Current.State == "submitted" {
		return classifyCanonicalV19HerdrSessionAcquireUncertain(ctx, homeDir, current, observed, now, nil)
	}
	return "uncertain", fmt.Errorf("reconcile canonical v19 Herdr SessionAcquire: operation remains uncertain: %s", observed.Reason)
}

func establishCanonicalV19ObservedHerdrSession(
	ctx context.Context,
	homeDir string,
	current canonicalV19HerdrSessionAcquireCurrent,
	observed canonicalV19HerdrSessionObservation,
	now func() time.Time,
) (string, error) {
	establishedAt := canonicalV19HerdrSessionTimestampAfter(now(), current.Current.StateChangedAt)
	if err := EstablishCanonicalV19SessionBinding(ctx, homeDir, CanonicalV19SessionBindingEvidence{
		OperationID:        current.Current.Request.OperationID,
		ProviderSessionKey: observed.ProviderSessionKey,
		EstablishedAt:      establishedAt,
		EvidenceDigest:     observed.EvidenceDigest,
	}); err != nil {
		return current.Current.State, err
	}
	return "succeeded", nil
}

func classifyCanonicalV19HerdrSessionAcquireNoEffect(
	ctx context.Context,
	homeDir string,
	current canonicalV19HerdrSessionAcquireCurrent,
	observed canonicalV19HerdrSessionObservation,
	now func() time.Time,
	reason string,
) (string, error) {
	observedAt := canonicalV19HerdrSessionTimestampAfter(now(), current.Current.StateChangedAt)
	if err := ClassifyCanonicalV19SessionAcquire(ctx, homeDir, CanonicalV19SessionAcquireTransitionInput{
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
	return "no-effect", fmt.Errorf("reconcile canonical v19 Herdr SessionAcquire: %s", reason)
}

func classifyCanonicalV19HerdrSessionAcquireUncertain(
	ctx context.Context,
	homeDir string,
	current canonicalV19HerdrSessionAcquireCurrent,
	observed canonicalV19HerdrSessionObservation,
	now func() time.Time,
	performErr error,
) (string, error) {
	observedAt := canonicalV19HerdrSessionTimestampAfter(now(), current.Current.StateChangedAt)
	if err := ClassifyCanonicalV19SessionAcquire(ctx, homeDir, CanonicalV19SessionAcquireTransitionInput{
		OperationID:    current.Current.Request.OperationID,
		State:          "uncertain",
		ObservedAt:     observedAt,
		EvidenceDigest: observed.EvidenceDigest,
	}); err != nil {
		return current.Current.State, err
	}
	reason := observed.Reason
	if performErr != nil {
		reason = canonicalV19HerdrSessionPerformFailure(reason, performErr)
	}
	if reason == "" {
		reason = "strongest provider evidence cannot classify the submitted acquisition"
	}
	return "uncertain", fmt.Errorf("reconcile canonical v19 Herdr SessionAcquire: operation is uncertain: %s", reason)
}

func performCanonicalV19HerdrSessionAcquire(
	current canonicalV19HerdrSessionAcquireCurrent,
	client canonicalV19HerdrSessionClient,
) (string, error) {
	workspace, tab, pane, err := client.WorkspaceCreate(
		current.WorktreePath,
		nil,
		canonicalV19HerdrSessionWorkspaceLabel(current.Current.Request.BindingID),
	)
	if err != nil {
		return "", err
	}
	return encodeCanonicalV19HerdrSessionProviderKey(canonicalV19HerdrSessionProviderKey{
		SessionName: herdr.SessionName(current.FleetID),
		WorkspaceID: workspace.WorkspaceID,
		TabID:       tab.TabID,
		PaneID:      pane.PaneID,
	})
}

func observeCanonicalV19HerdrSessionAcquire(
	ctx context.Context,
	current canonicalV19HerdrSessionAcquireCurrent,
	providerKey string,
	client canonicalV19HerdrSessionClient,
) canonicalV19HerdrSessionObservation {
	request := current.Current.Request
	sessionName := herdr.SessionName(current.FleetID)
	label := canonicalV19HerdrSessionWorkspaceLabel(request.BindingID)
	session := client.ObserveSession(ctx)
	if session.Name != sessionName || session.State != herdr.SessionRunningCompatible {
		reason := fmt.Sprintf("exact Herdr session %q is %q", sessionName, session.State)
		if session.Reason != "" {
			reason += ": " + session.Reason
		}
		return finalizeCanonicalV19HerdrSessionObservation(current, canonicalV19HerdrSessionObservation{
			State: canonicalV19HerdrSessionUnknown, Reason: reason,
		})
	}

	workspaces, err := client.WorkspaceListContext(ctx)
	if err != nil {
		return finalizeCanonicalV19HerdrSessionObservation(current, canonicalV19HerdrSessionObservation{
			State: canonicalV19HerdrSessionUnknown, Reason: "list exact Herdr session workspaces: " + canonicalV19HerdrSessionErrorText(err),
		})
	}
	if providerKey == "" {
		matches := 0
		for _, workspace := range workspaces {
			if workspace.Label == label {
				matches++
			}
		}
		if matches == 0 {
			return finalizeCanonicalV19HerdrSessionObservation(current, canonicalV19HerdrSessionObservation{State: canonicalV19HerdrSessionAbsent})
		}
		return finalizeCanonicalV19HerdrSessionObservation(current, canonicalV19HerdrSessionObservation{
			State:        canonicalV19HerdrSessionMismatch,
			LabelMatches: matches,
			Reason:       "dedicated Session workspace locator exists but exact provider-assigned tab/pane identity is not durably known",
		})
	}

	key, err := parseCanonicalV19HerdrSessionProviderKey(providerKey)
	if err != nil {
		return finalizeCanonicalV19HerdrSessionObservation(current, canonicalV19HerdrSessionObservation{
			State: canonicalV19HerdrSessionMismatch, ProviderSessionKey: providerKey,
			Reason: "provider Session key is invalid: " + err.Error(),
		})
	}
	observation := canonicalV19HerdrSessionObservation{
		ProviderSessionKey: providerKey,
		WorkspaceID:        key.WorkspaceID,
		TabID:              key.TabID,
		PaneID:             key.PaneID,
	}
	if key.SessionName != sessionName {
		observation.State = canonicalV19HerdrSessionMismatch
		observation.Reason = fmt.Sprintf("provider Session key names Herdr session %q, want %q", key.SessionName, sessionName)
		return finalizeCanonicalV19HerdrSessionObservation(current, observation)
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
			return finalizeCanonicalV19HerdrSessionObservation(current, observation)
		}
		if paneErr != nil {
			observation.State = canonicalV19HerdrSessionUnknown
			observation.Reason = "workspace absent but exact pane could not be disproven: " + canonicalV19HerdrSessionErrorText(paneErr)
			return finalizeCanonicalV19HerdrSessionObservation(current, observation)
		}
		observation.State = canonicalV19HerdrSessionMismatch
		observation.PaneCwd = pane.Cwd
		observation.Reason = "exact pane remains live while its provider Session key workspace is absent from inventory"
		return finalizeCanonicalV19HerdrSessionObservation(current, observation)
	}
	if workspaceMatches != 1 {
		observation.State = canonicalV19HerdrSessionUnknown
		observation.Reason = "Herdr workspace inventory returned the exact workspace identity more than once"
		return finalizeCanonicalV19HerdrSessionObservation(current, observation)
	}
	if workspace.Label != label {
		observation.State = canonicalV19HerdrSessionMismatch
		observation.Reason = "exact workspace identity no longer carries the dedicated Session locator"
		return finalizeCanonicalV19HerdrSessionObservation(current, observation)
	}

	tabs, err := client.TabList(key.WorkspaceID)
	if err != nil {
		observation.State = canonicalV19HerdrSessionUnknown
		observation.Reason = "list exact Herdr workspace tabs: " + canonicalV19HerdrSessionErrorText(err)
		return finalizeCanonicalV19HerdrSessionObservation(current, observation)
	}
	if len(tabs) != 1 {
		observation.State = canonicalV19HerdrSessionMismatch
		observation.Reason = fmt.Sprintf("dedicated Session workspace has %d tabs, want exactly 1", len(tabs))
		return finalizeCanonicalV19HerdrSessionObservation(current, observation)
	}
	if tabs[0].TabID != key.TabID || tabs[0].WorkspaceID != key.WorkspaceID {
		observation.State = canonicalV19HerdrSessionMismatch
		observation.Reason = "dedicated Session workspace root tab identity differs from the provider Session key"
		return finalizeCanonicalV19HerdrSessionObservation(current, observation)
	}

	pane, err := client.PaneGetContext(ctx, key.PaneID)
	if errors.Is(err, herdr.ErrNotFound) {
		observation.State = canonicalV19HerdrSessionMismatch
		observation.Reason = "provider Session key workspace/tab remain but the exact root pane is absent"
		return finalizeCanonicalV19HerdrSessionObservation(current, observation)
	}
	if err != nil {
		observation.State = canonicalV19HerdrSessionUnknown
		observation.Reason = "observe exact Herdr root pane: " + canonicalV19HerdrSessionErrorText(err)
		return finalizeCanonicalV19HerdrSessionObservation(current, observation)
	}
	observation.PaneCwd = pane.Cwd
	if pane.PaneID != key.PaneID || pane.TabID != key.TabID || pane.WorkspaceID != key.WorkspaceID {
		observation.State = canonicalV19HerdrSessionMismatch
		observation.Reason = "exact root pane parent identities differ from the provider Session key"
		return finalizeCanonicalV19HerdrSessionObservation(current, observation)
	}
	if pane.Cwd == "" || !handgit.SamePath(pane.Cwd, current.WorktreePath) {
		observation.State = canonicalV19HerdrSessionMismatch
		observation.Reason = "exact root pane cwd differs from the immutable WorktreeBinding path"
		return finalizeCanonicalV19HerdrSessionObservation(current, observation)
	}
	if pane.Agent != "" {
		observation.State = canonicalV19HerdrSessionMismatch
		observation.Reason = "exact Session pane already contains a provider-detected worker before canonical Launch"
		return finalizeCanonicalV19HerdrSessionObservation(current, observation)
	}
	observation.State = canonicalV19HerdrSessionExact
	return finalizeCanonicalV19HerdrSessionObservation(current, observation)
}

func readCanonicalV19HerdrSessionAcquireCurrent(
	ctx context.Context,
	homeDir string,
	operationID string,
) (canonicalV19HerdrSessionAcquireCurrent, error) {
	db, err := openReadOnly(homeDir)
	if err != nil {
		return canonicalV19HerdrSessionAcquireCurrent{}, err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19HerdrSessionAcquireCurrent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := loadCanonicalV19SessionAcquireCurrent(ctx, tx, operationID)
	if err != nil {
		return canonicalV19HerdrSessionAcquireCurrent{}, err
	}
	result := canonicalV19HerdrSessionAcquireCurrent{Current: current}
	if err := tx.QueryRowContext(ctx, `SELECT p.fleet_id,b.path
		FROM project p
		JOIN attempt_worktree_binding b ON b.id=? AND b.attempt_id=?
		WHERE p.id=?`, current.Request.WorktreeBindingID, current.Request.AttemptID, current.Request.ProjectID).Scan(
		&result.FleetID, &result.WorktreePath,
	); err != nil {
		return canonicalV19HerdrSessionAcquireCurrent{}, fmt.Errorf("read canonical v19 Herdr SessionAcquire provider context: %w", err)
	}
	if result.FleetID == "" || result.WorktreePath == "" {
		return canonicalV19HerdrSessionAcquireCurrent{}, fmt.Errorf("read canonical v19 Herdr SessionAcquire provider context: Fleet ID or WorktreeBinding path is empty")
	}
	return result, nil
}

func canonicalV19HerdrSessionWorkspaceLabel(bindingID string) string {
	digest := sha256.Sum256([]byte(bindingID))
	return "hand:v19:session:" + hex.EncodeToString(digest[:])
}

func encodeCanonicalV19HerdrSessionProviderKey(key canonicalV19HerdrSessionProviderKey) (string, error) {
	for name, value := range map[string]string{
		"session": key.SessionName, "workspace": key.WorkspaceID, "tab": key.TabID, "pane": key.PaneID,
	} {
		if value == "" {
			return "", fmt.Errorf("Herdr provider Session key %s is empty", name)
		}
	}
	values := url.Values{}
	values.Set("pane", key.PaneID)
	values.Set("session", key.SessionName)
	values.Set("tab", key.TabID)
	values.Set("workspace", key.WorkspaceID)
	return "herdr:v1?" + values.Encode(), nil
}

func parseCanonicalV19HerdrSessionProviderKey(value string) (canonicalV19HerdrSessionProviderKey, error) {
	const prefix = "herdr:v1?"
	if !strings.HasPrefix(value, prefix) {
		return canonicalV19HerdrSessionProviderKey{}, fmt.Errorf("missing %q prefix", prefix)
	}
	values, err := url.ParseQuery(strings.TrimPrefix(value, prefix))
	if err != nil {
		return canonicalV19HerdrSessionProviderKey{}, fmt.Errorf("parse query: %w", err)
	}
	if len(values) != 4 {
		return canonicalV19HerdrSessionProviderKey{}, fmt.Errorf("want exactly session/workspace/tab/pane fields")
	}
	one := func(name string) (string, error) {
		items := values[name]
		if len(items) != 1 || items[0] == "" {
			return "", fmt.Errorf("field %q must occur exactly once and be non-empty", name)
		}
		return items[0], nil
	}
	var key canonicalV19HerdrSessionProviderKey
	if key.SessionName, err = one("session"); err != nil {
		return canonicalV19HerdrSessionProviderKey{}, err
	}
	if key.WorkspaceID, err = one("workspace"); err != nil {
		return canonicalV19HerdrSessionProviderKey{}, err
	}
	if key.TabID, err = one("tab"); err != nil {
		return canonicalV19HerdrSessionProviderKey{}, err
	}
	if key.PaneID, err = one("pane"); err != nil {
		return canonicalV19HerdrSessionProviderKey{}, err
	}
	return key, nil
}

func finalizeCanonicalV19HerdrSessionObservation(
	current canonicalV19HerdrSessionAcquireCurrent,
	observed canonicalV19HerdrSessionObservation,
) canonicalV19HerdrSessionObservation {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:herdr-session-acquire-observation:v1")
	writeCanonicalV19DigestField(hash, "operation_id", current.Current.Request.OperationID)
	writeCanonicalV19DigestField(hash, "request_digest", current.Current.Request.RequestDigest)
	writeCanonicalV19DigestField(hash, "fleet_id", current.FleetID)
	writeCanonicalV19DigestField(hash, "worktree_path", current.WorktreePath)
	writeCanonicalV19DigestField(hash, "workspace_label", canonicalV19HerdrSessionWorkspaceLabel(current.Current.Request.BindingID))
	writeCanonicalV19DigestField(hash, "state", string(observed.State))
	writeCanonicalV19DigestField(hash, "provider_session_key", observed.ProviderSessionKey)
	writeCanonicalV19DigestField(hash, "workspace_id", observed.WorkspaceID)
	writeCanonicalV19DigestField(hash, "tab_id", observed.TabID)
	writeCanonicalV19DigestField(hash, "pane_id", observed.PaneID)
	writeCanonicalV19DigestField(hash, "pane_cwd", observed.PaneCwd)
	writeCanonicalV19DigestField(hash, "label_matches", fmt.Sprintf("%d", observed.LabelMatches))
	writeCanonicalV19DigestField(hash, "reason", observed.Reason)
	observed.EvidenceDigest = hex.EncodeToString(hash.Sum(nil))
	return observed
}

func canonicalV19HerdrSessionTimestampAfter(now time.Time, previous string) string {
	candidate := now.UTC()
	if prior, err := time.Parse(time.RFC3339Nano, previous); err == nil && !candidate.After(prior) {
		candidate = prior.Add(time.Nanosecond)
	}
	return candidate.Format(time.RFC3339Nano)
}

func canonicalV19HerdrSessionPerformFailure(prefix string, err error) string {
	if err == nil {
		return prefix
	}
	failure := "Herdr Session acquisition failed: " + canonicalV19HerdrSessionErrorText(err)
	if prefix == "" {
		return failure
	}
	return prefix + "; " + failure
}

func canonicalV19HerdrSessionErrorText(err error) string {
	if err == nil {
		return ""
	}
	text := strings.Join(strings.Fields(err.Error()), " ")
	if len(text) > 240 {
		text = text[:240]
	}
	return text
}
