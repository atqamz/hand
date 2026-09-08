package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	handgit "github.com/atqamz/hand/internal/git"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/launch"
)

type canonicalV19HerdrLaunchObservationState string

const (
	canonicalV19HerdrLaunchReady    canonicalV19HerdrLaunchObservationState = "ready"
	canonicalV19HerdrLaunchRunning  canonicalV19HerdrLaunchObservationState = "running"
	canonicalV19HerdrLaunchMismatch canonicalV19HerdrLaunchObservationState = "mismatch"
	canonicalV19HerdrLaunchUnknown  canonicalV19HerdrLaunchObservationState = "unknown"
)

type canonicalV19HerdrExecutorProviderKey struct {
	SessionName   string
	WorkspaceID   string
	TabID         string
	PaneID        string
	ProcessGroup  int
	ProcessID     int
	ProcessDigest string
}

type canonicalV19HerdrLaunchObservation struct {
	State               canonicalV19HerdrLaunchObservationState
	ProviderExecutorKey string
	WorkspaceID         string
	TabID               string
	PaneID              string
	PaneCwd             string
	ShellPID            int
	ProcessGroupID      int
	ProcessID           int
	ProcessDigest       string
	EvidenceDigest      string
	Reason              string
}

type canonicalV19HerdrLaunchCurrent struct {
	Current            canonicalV19LaunchCurrent
	FleetID            string
	WorktreePath       string
	ProviderSessionKey string
}

type canonicalV19HerdrLaunchClient interface {
	ObserveSession(context.Context) herdr.SessionObservation
	WorkspaceListContext(context.Context) ([]herdr.Workspace, error)
	TabList(string) ([]herdr.Tab, error)
	PaneGetContext(context.Context, string) (herdr.Pane, error)
	PaneProcessInfo(string) (herdr.ProcessInfo, error)
	PaneRunExactSpec(string, launch.LaunchSpec) error
}

type canonicalV19HerdrLaunchDeps struct {
	clientFor func(string) canonicalV19HerdrLaunchClient
	now       func() time.Time
}

// ReconcileCanonicalV19HerdrLaunch reconciles one exact canonical v19 Launch against Herdr.
// It commits submitted immediately before pane-run mutation, establishes an ExecutorBinding only
// from exact process identity/argv/cwd evidence, and never blindly relaunches submitted/uncertain work.
func ReconcileCanonicalV19HerdrLaunch(ctx context.Context, homeDir, operationID string) (string, error) {
	return reconcileCanonicalV19HerdrLaunch(ctx, homeDir, operationID, canonicalV19HerdrLaunchDeps{
		clientFor: func(sessionName string) canonicalV19HerdrLaunchClient {
			return herdr.NewManagedSessionClient(sessionName)
		},
		now: time.Now,
	})
}

func reconcileCanonicalV19HerdrLaunch(
	ctx context.Context,
	homeDir string,
	operationID string,
	deps canonicalV19HerdrLaunchDeps,
) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if operationID == "" {
		return "", fmt.Errorf("reconcile canonical v19 Herdr Launch: operation ID is empty")
	}
	if deps.clientFor == nil || deps.now == nil {
		return "", fmt.Errorf("reconcile canonical v19 Herdr Launch: adapter dependencies are incomplete")
	}

	current, err := readCanonicalV19HerdrLaunchCurrent(ctx, homeDir, operationID)
	if err != nil {
		if errors.Is(err, ErrCanonicalV19LaunchNotCurrent) {
			if state, found, terminalErr := readCanonicalV19HerdrLaunchTerminal(ctx, homeDir, operationID); terminalErr != nil {
				return "", fmt.Errorf("reconcile canonical v19 Herdr Launch: %w", terminalErr)
			} else if found {
				return state, nil
			}
		}
		return "", fmt.Errorf("reconcile canonical v19 Herdr Launch: %w", err)
	}
	request := current.Current.Request
	switch current.Current.State {
	case "succeeded", "rejected", "no-effect":
		return current.Current.State, nil
	case "prepared", "submitted", "uncertain":
	default:
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr Launch: %w: operation %q is %q",
			ErrCanonicalV19LaunchTransition, operationID, current.Current.State)
	}
	if request.AdapterRef != canonicalV19HerdrSessionAdapterRef {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr Launch: %w: adapter %q is not %q",
			ErrCanonicalV19LaunchNotCurrent, request.AdapterRef, canonicalV19HerdrSessionAdapterRef)
	}
	key, err := parseCanonicalV19HerdrSessionProviderKey(current.ProviderSessionKey)
	if err != nil {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr Launch: %w: invalid provider Session key: %v",
			ErrCanonicalV19LaunchNotCurrent, err)
	}
	expectedSession := herdr.SessionName(current.FleetID)
	if key.SessionName != expectedSession {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr Launch: %w: provider Session key names %q, want %q",
			ErrCanonicalV19LaunchNotCurrent, key.SessionName, expectedSession)
	}

	providerSpec, specErr := canonicalV19HerdrLiteralLaunchSpec(request.Spec)
	if specErr != nil && current.Current.State == "prepared" {
		return "prepared", fmt.Errorf("reconcile canonical v19 Herdr Launch: %w", specErr)
	}
	client := deps.clientFor(expectedSession)
	if client == nil {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr Launch: provider client is unavailable")
	}
	observed := observeCanonicalV19HerdrLaunch(ctx, current, client)
	if current.Current.State != "prepared" {
		if specErr != nil {
			observed.Reason = canonicalV19HerdrLaunchJoinReason(observed.Reason, specErr.Error())
			observed = finalizeCanonicalV19HerdrLaunchObservation(current, observed)
		}
		return reconcileCanonicalV19SubmittedHerdrLaunch(ctx, homeDir, current, observed, deps.now, specErr == nil)
	}

	switch observed.State {
	case canonicalV19HerdrLaunchReady:
	case canonicalV19HerdrLaunchRunning:
		return "prepared", fmt.Errorf("reconcile canonical v19 Herdr Launch: exact target process already exists before submission")
	case canonicalV19HerdrLaunchMismatch:
		return "prepared", fmt.Errorf("reconcile canonical v19 Herdr Launch: provider ownership is unresolved: %s", observed.Reason)
	case canonicalV19HerdrLaunchUnknown:
		return "prepared", fmt.Errorf("reconcile canonical v19 Herdr Launch: provider observation is unknown: %s", observed.Reason)
	default:
		return "prepared", fmt.Errorf("reconcile canonical v19 Herdr Launch: provider observation state %q is invalid", observed.State)
	}

	submittedAt := canonicalV19HerdrSessionTimestampAfter(deps.now(), current.Current.StateChangedAt)
	submitted, err := SubmitCanonicalV19Launch(ctx, homeDir, operationID, submittedAt, observed.EvidenceDigest)
	if err != nil {
		return "prepared", err
	}
	current.Current.Request = submitted
	current.Current.State = "submitted"
	current.Current.StateChangedAt = submittedAt

	observed = observeCanonicalV19HerdrLaunch(ctx, current, client)
	switch observed.State {
	case canonicalV19HerdrLaunchRunning:
		return establishCanonicalV19ObservedHerdrExecutor(ctx, homeDir, current, observed, deps.now)
	case canonicalV19HerdrLaunchReady:
	case canonicalV19HerdrLaunchMismatch, canonicalV19HerdrLaunchUnknown:
		return classifyCanonicalV19HerdrLaunchUncertain(ctx, homeDir, current, observed, deps.now, nil)
	default:
		return "submitted", fmt.Errorf("reconcile canonical v19 Herdr Launch: provider observation state %q is invalid", observed.State)
	}

	performErr := client.PaneRunExactSpec(key.PaneID, providerSpec)
	observed = observeCanonicalV19HerdrLaunch(ctx, current, client)
	switch observed.State {
	case canonicalV19HerdrLaunchRunning:
		return establishCanonicalV19ObservedHerdrExecutor(ctx, homeDir, current, observed, deps.now)
	case canonicalV19HerdrLaunchReady:
		if performErr != nil && herdr.IsProcessNotStarted(performErr) {
			return classifyCanonicalV19HerdrLaunchNoEffect(ctx, homeDir, current, observed, deps.now,
				"Herdr launch mutation did not start and the exact Session pane remains at its pre-launch shell state")
		}
		return classifyCanonicalV19HerdrLaunchUncertain(ctx, homeDir, current, observed, deps.now, performErr)
	case canonicalV19HerdrLaunchMismatch, canonicalV19HerdrLaunchUnknown:
		return classifyCanonicalV19HerdrLaunchUncertain(ctx, homeDir, current, observed, deps.now, performErr)
	default:
		return "submitted", fmt.Errorf("reconcile canonical v19 Herdr Launch: provider observation state %q is invalid", observed.State)
	}
}

func reconcileCanonicalV19SubmittedHerdrLaunch(
	ctx context.Context,
	homeDir string,
	current canonicalV19HerdrLaunchCurrent,
	observed canonicalV19HerdrLaunchObservation,
	now func() time.Time,
	environmentResolved bool,
) (string, error) {
	if observed.State == canonicalV19HerdrLaunchRunning && environmentResolved {
		return establishCanonicalV19ObservedHerdrExecutor(ctx, homeDir, current, observed, now)
	}
	if observed.Reason == "" {
		observed.Reason = "submitted Launch cannot be replayed without positive exact ExecutorBinding evidence"
	} else {
		observed.Reason += "; submitted Launch cannot be replayed without positive exact ExecutorBinding evidence"
	}
	observed = finalizeCanonicalV19HerdrLaunchObservation(current, observed)
	if current.Current.State == "submitted" {
		return classifyCanonicalV19HerdrLaunchUncertain(ctx, homeDir, current, observed, now, nil)
	}
	return "uncertain", fmt.Errorf("reconcile canonical v19 Herdr Launch: operation remains uncertain: %s", observed.Reason)
}

func establishCanonicalV19ObservedHerdrExecutor(
	ctx context.Context,
	homeDir string,
	current canonicalV19HerdrLaunchCurrent,
	observed canonicalV19HerdrLaunchObservation,
	now func() time.Time,
) (string, error) {
	if observed.ProviderExecutorKey == "" {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr Launch: exact process observation lacks provider Executor key")
	}
	establishedAt := canonicalV19HerdrSessionTimestampAfter(now(), current.Current.StateChangedAt)
	if err := EstablishCanonicalV19ExecutorBinding(ctx, homeDir, CanonicalV19ExecutorBindingEvidence{
		OperationID:         current.Current.Request.OperationID,
		ProviderExecutorKey: observed.ProviderExecutorKey,
		EstablishedAt:       establishedAt,
		EvidenceDigest:      observed.EvidenceDigest,
	}); err != nil {
		return current.Current.State, err
	}
	return "succeeded", nil
}

func classifyCanonicalV19HerdrLaunchNoEffect(
	ctx context.Context,
	homeDir string,
	current canonicalV19HerdrLaunchCurrent,
	observed canonicalV19HerdrLaunchObservation,
	now func() time.Time,
	reason string,
) (string, error) {
	observedAt := canonicalV19HerdrSessionTimestampAfter(now(), current.Current.StateChangedAt)
	if err := ClassifyCanonicalV19Launch(ctx, homeDir, CanonicalV19LaunchTransitionInput{
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
	return "no-effect", fmt.Errorf("reconcile canonical v19 Herdr Launch: %s", reason)
}

func classifyCanonicalV19HerdrLaunchUncertain(
	ctx context.Context,
	homeDir string,
	current canonicalV19HerdrLaunchCurrent,
	observed canonicalV19HerdrLaunchObservation,
	now func() time.Time,
	performErr error,
) (string, error) {
	if current.Current.State == "uncertain" {
		reason := observed.Reason
		if reason == "" {
			reason = "strongest provider evidence still cannot classify the Launch"
		}
		return "uncertain", fmt.Errorf("reconcile canonical v19 Herdr Launch: operation remains uncertain: %s", reason)
	}
	observedAt := canonicalV19HerdrSessionTimestampAfter(now(), current.Current.StateChangedAt)
	if err := ClassifyCanonicalV19Launch(ctx, homeDir, CanonicalV19LaunchTransitionInput{
		OperationID:    current.Current.Request.OperationID,
		State:          "uncertain",
		ObservedAt:     observedAt,
		EvidenceDigest: observed.EvidenceDigest,
	}); err != nil {
		return current.Current.State, err
	}
	reason := observed.Reason
	if performErr != nil {
		reason = canonicalV19HerdrLaunchJoinReason(reason, "Herdr exact Launch failed: "+canonicalV19HerdrSessionErrorText(performErr))
	}
	if reason == "" {
		reason = "strongest provider evidence cannot classify the submitted Launch"
	}
	return "uncertain", fmt.Errorf("reconcile canonical v19 Herdr Launch: operation is uncertain: %s", reason)
}

func canonicalV19HerdrLiteralLaunchSpec(spec CanonicalV19LaunchSpec) (launch.LaunchSpec, error) {
	provider := launch.LaunchSpec{
		Executable: spec.Executable,
		Args:       append([]string(nil), spec.Arguments...),
		Env:        make(map[string]string, len(spec.Environment)),
		Cwd:        spec.Cwd,
	}
	for name, value := range spec.Environment {
		switch value.ValueKind {
		case "literal":
			provider.Env[name] = value.ValueMaterial
		case "secret-ref":
			return launch.LaunchSpec{}, fmt.Errorf("launch environment %q uses unresolved secret-ref %q; no canonical v19 secret resolver is available", name, value.ValueMaterial)
		default:
			return launch.LaunchSpec{}, fmt.Errorf("launch environment %q has unsupported value kind %q", name, value.ValueKind)
		}
	}
	validated, err := launch.NewSpec(provider)
	if err != nil {
		return launch.LaunchSpec{}, fmt.Errorf("convert persisted LaunchSpec: %w", err)
	}
	return validated, nil
}

func observeCanonicalV19HerdrLaunch(
	ctx context.Context,
	current canonicalV19HerdrLaunchCurrent,
	client canonicalV19HerdrLaunchClient,
) canonicalV19HerdrLaunchObservation {
	request := current.Current.Request
	key, err := parseCanonicalV19HerdrSessionProviderKey(current.ProviderSessionKey)
	if err != nil {
		return finalizeCanonicalV19HerdrLaunchObservation(current, canonicalV19HerdrLaunchObservation{
			State: canonicalV19HerdrLaunchMismatch, Reason: "provider Session key is invalid: " + err.Error(),
		})
	}
	observed := canonicalV19HerdrLaunchObservation{WorkspaceID: key.WorkspaceID, TabID: key.TabID, PaneID: key.PaneID}
	sessionName := herdr.SessionName(current.FleetID)
	session := client.ObserveSession(ctx)
	if session.Name != sessionName || session.State != herdr.SessionRunningCompatible {
		reason := fmt.Sprintf("exact Herdr session %q is %q", sessionName, session.State)
		if session.Reason != "" {
			reason += ": " + session.Reason
		}
		observed.State = canonicalV19HerdrLaunchUnknown
		observed.Reason = reason
		return finalizeCanonicalV19HerdrLaunchObservation(current, observed)
	}

	workspaces, err := client.WorkspaceListContext(ctx)
	if err != nil {
		observed.State = canonicalV19HerdrLaunchUnknown
		observed.Reason = "list exact Herdr session workspaces: " + canonicalV19HerdrSessionErrorText(err)
		return finalizeCanonicalV19HerdrLaunchObservation(current, observed)
	}
	workspaceMatches := 0
	var workspace herdr.Workspace
	for _, candidate := range workspaces {
		if candidate.WorkspaceID == key.WorkspaceID {
			workspace = candidate
			workspaceMatches++
		}
	}
	if workspaceMatches != 1 {
		observed.State = canonicalV19HerdrLaunchMismatch
		if workspaceMatches == 0 {
			observed.Reason = "exact Session workspace is absent"
		} else {
			observed.Reason = "Herdr workspace inventory returned the exact Session workspace more than once"
		}
		return finalizeCanonicalV19HerdrLaunchObservation(current, observed)
	}
	if workspace.Label != canonicalV19HerdrSessionWorkspaceLabel(request.SessionBindingID) {
		observed.State = canonicalV19HerdrLaunchMismatch
		observed.Reason = "exact workspace identity no longer carries the SessionBinding locator"
		return finalizeCanonicalV19HerdrLaunchObservation(current, observed)
	}

	tabs, err := client.TabList(key.WorkspaceID)
	if err != nil {
		observed.State = canonicalV19HerdrLaunchUnknown
		observed.Reason = "list exact Session workspace tabs: " + canonicalV19HerdrSessionErrorText(err)
		return finalizeCanonicalV19HerdrLaunchObservation(current, observed)
	}
	if len(tabs) != 1 || tabs[0].TabID != key.TabID || tabs[0].WorkspaceID != key.WorkspaceID {
		observed.State = canonicalV19HerdrLaunchMismatch
		observed.Reason = "Session workspace root tab no longer matches the persisted provider Session key"
		return finalizeCanonicalV19HerdrLaunchObservation(current, observed)
	}

	pane, err := client.PaneGetContext(ctx, key.PaneID)
	if err != nil {
		if errors.Is(err, herdr.ErrNotFound) {
			observed.State = canonicalV19HerdrLaunchMismatch
			observed.Reason = "exact Session root pane is absent"
		} else {
			observed.State = canonicalV19HerdrLaunchUnknown
			observed.Reason = "observe exact Session root pane: " + canonicalV19HerdrSessionErrorText(err)
		}
		return finalizeCanonicalV19HerdrLaunchObservation(current, observed)
	}
	observed.PaneCwd = pane.Cwd
	if pane.PaneID != key.PaneID || pane.TabID != key.TabID || pane.WorkspaceID != key.WorkspaceID {
		observed.State = canonicalV19HerdrLaunchMismatch
		observed.Reason = "exact root pane parent identities differ from the provider Session key"
		return finalizeCanonicalV19HerdrLaunchObservation(current, observed)
	}
	if pane.Cwd == "" || !handgit.SamePath(pane.Cwd, current.WorktreePath) || !handgit.SamePath(pane.Cwd, request.Spec.Cwd) {
		observed.State = canonicalV19HerdrLaunchMismatch
		observed.Reason = "exact root pane cwd differs from the immutable WorktreeBinding/Launch cwd"
		return finalizeCanonicalV19HerdrLaunchObservation(current, observed)
	}

	info, err := client.PaneProcessInfo(key.PaneID)
	if err != nil {
		observed.State = canonicalV19HerdrLaunchUnknown
		observed.Reason = "observe exact pane process identity: " + canonicalV19HerdrSessionErrorText(err)
		return finalizeCanonicalV19HerdrLaunchObservation(current, observed)
	}
	observed.ShellPID = info.ShellPID
	observed.ProcessGroupID = info.ForegroundProcessGroupID
	if info.PaneID != key.PaneID || info.ShellPID <= 0 || info.ForegroundProcessGroupID <= 0 {
		observed.State = canonicalV19HerdrLaunchUnknown
		observed.Reason = "pane process evidence lacks exact pane/shell/process-group identity"
		return finalizeCanonicalV19HerdrLaunchObservation(current, observed)
	}

	shellObserved := false
	foreignForeground := false
	targets := make([]herdr.Process, 0, 1)
	for _, process := range info.ForegroundProcesses {
		if process.PID == info.ShellPID {
			shellObserved = true
			continue
		}
		if canonicalV19HerdrProcessMatchesLaunch(process, request.Spec) {
			targets = append(targets, process)
			continue
		}
		if process.PID > 0 {
			foreignForeground = true
		}
	}
	if !shellObserved {
		observed.State = canonicalV19HerdrLaunchUnknown
		observed.Reason = "pane process evidence does not contain the exact shell PID"
		return finalizeCanonicalV19HerdrLaunchObservation(current, observed)
	}
	if len(targets) > 1 {
		observed.State = canonicalV19HerdrLaunchMismatch
		observed.Reason = "foreground process group contains the exact Launch process more than once"
		return finalizeCanonicalV19HerdrLaunchObservation(current, observed)
	}
	if len(targets) == 1 {
		target := targets[0]
		observed.ProcessID = target.PID
		observed.ProcessDigest = canonicalV19HerdrProcessDigest(target)
		providerKey, err := encodeCanonicalV19HerdrExecutorProviderKey(canonicalV19HerdrExecutorProviderKey{
			SessionName: key.SessionName, WorkspaceID: key.WorkspaceID, TabID: key.TabID, PaneID: key.PaneID,
			ProcessGroup: info.ForegroundProcessGroupID, ProcessID: target.PID, ProcessDigest: observed.ProcessDigest,
		})
		if err != nil {
			observed.State = canonicalV19HerdrLaunchUnknown
			observed.Reason = "encode exact provider Executor key: " + err.Error()
			return finalizeCanonicalV19HerdrLaunchObservation(current, observed)
		}
		observed.ProviderExecutorKey = providerKey
		observed.State = canonicalV19HerdrLaunchRunning
		return finalizeCanonicalV19HerdrLaunchObservation(current, observed)
	}
	if foreignForeground || pane.Agent != "" {
		observed.State = canonicalV19HerdrLaunchMismatch
		observed.Reason = "Session pane contains a foreground/provider-detected process that does not match the exact LaunchSpec"
		return finalizeCanonicalV19HerdrLaunchObservation(current, observed)
	}
	observed.State = canonicalV19HerdrLaunchReady
	return finalizeCanonicalV19HerdrLaunchObservation(current, observed)
}

func canonicalV19HerdrProcessMatchesLaunch(process herdr.Process, spec CanonicalV19LaunchSpec) bool {
	if process.PID <= 0 || len(process.Argv) != len(spec.Arguments)+1 || process.Cwd == "" {
		return false
	}
	if canonicalV19HerdrExecutableBase(process.Argv[0]) != canonicalV19HerdrExecutableBase(spec.Executable) {
		return false
	}
	for i, argument := range spec.Arguments {
		if process.Argv[i+1] != argument {
			return false
		}
	}
	return handgit.SamePath(process.Cwd, spec.Cwd)
}

func canonicalV19HerdrExecutableBase(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.TrimPrefix(filepath.Base(value), "-")
	if len(value) >= len(".exe") && strings.EqualFold(value[len(value)-len(".exe"):], ".exe") {
		value = value[:len(value)-len(".exe")]
	}
	return value
}

func canonicalV19HerdrProcessDigest(process herdr.Process) string {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:herdr-executor-process:v1")
	writeCanonicalV19DigestField(hash, "pid", strconv.Itoa(process.PID))
	writeCanonicalV19DigestField(hash, "cwd", process.Cwd)
	writeCanonicalV19DigestField(hash, "argv_count", strconv.Itoa(len(process.Argv)))
	for i, value := range process.Argv {
		writeCanonicalV19DigestField(hash, fmt.Sprintf("argv_%d", i), value)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func encodeCanonicalV19HerdrExecutorProviderKey(key canonicalV19HerdrExecutorProviderKey) (string, error) {
	for name, value := range map[string]string{
		"session": key.SessionName, "workspace": key.WorkspaceID, "tab": key.TabID, "pane": key.PaneID,
		"process digest": key.ProcessDigest,
	} {
		if value == "" {
			return "", fmt.Errorf("Herdr provider Executor key %s is empty", name)
		}
	}
	if key.ProcessGroup <= 0 || key.ProcessID <= 0 {
		return "", fmt.Errorf("Herdr provider Executor key process identity is invalid")
	}
	values := url.Values{}
	values.Set("digest", key.ProcessDigest)
	values.Set("pane", key.PaneID)
	values.Set("pgid", strconv.Itoa(key.ProcessGroup))
	values.Set("pid", strconv.Itoa(key.ProcessID))
	values.Set("session", key.SessionName)
	values.Set("tab", key.TabID)
	values.Set("workspace", key.WorkspaceID)
	return "herdr-executor:v1?" + values.Encode(), nil
}

func parseCanonicalV19HerdrExecutorProviderKey(value string) (canonicalV19HerdrExecutorProviderKey, error) {
	const prefix = "herdr-executor:v1?"
	if !strings.HasPrefix(value, prefix) {
		return canonicalV19HerdrExecutorProviderKey{}, fmt.Errorf("missing %q prefix", prefix)
	}
	values, err := url.ParseQuery(strings.TrimPrefix(value, prefix))
	if err != nil {
		return canonicalV19HerdrExecutorProviderKey{}, fmt.Errorf("parse query: %w", err)
	}
	if len(values) != 7 {
		return canonicalV19HerdrExecutorProviderKey{}, fmt.Errorf("want exactly session/workspace/tab/pane/pgid/pid/digest fields")
	}
	one := func(name string) (string, error) {
		items := values[name]
		if len(items) != 1 || items[0] == "" {
			return "", fmt.Errorf("field %q must occur exactly once and be non-empty", name)
		}
		return items[0], nil
	}
	var key canonicalV19HerdrExecutorProviderKey
	if key.SessionName, err = one("session"); err != nil {
		return canonicalV19HerdrExecutorProviderKey{}, err
	}
	if key.WorkspaceID, err = one("workspace"); err != nil {
		return canonicalV19HerdrExecutorProviderKey{}, err
	}
	if key.TabID, err = one("tab"); err != nil {
		return canonicalV19HerdrExecutorProviderKey{}, err
	}
	if key.PaneID, err = one("pane"); err != nil {
		return canonicalV19HerdrExecutorProviderKey{}, err
	}
	if key.ProcessDigest, err = one("digest"); err != nil {
		return canonicalV19HerdrExecutorProviderKey{}, err
	}
	pgid, err := one("pgid")
	if err != nil {
		return canonicalV19HerdrExecutorProviderKey{}, err
	}
	if key.ProcessGroup, err = strconv.Atoi(pgid); err != nil || key.ProcessGroup <= 0 {
		return canonicalV19HerdrExecutorProviderKey{}, fmt.Errorf("field %q must be a positive integer", "pgid")
	}
	pid, err := one("pid")
	if err != nil {
		return canonicalV19HerdrExecutorProviderKey{}, err
	}
	if key.ProcessID, err = strconv.Atoi(pid); err != nil || key.ProcessID <= 0 {
		return canonicalV19HerdrExecutorProviderKey{}, fmt.Errorf("field %q must be a positive integer", "pid")
	}
	return key, nil
}

func readCanonicalV19HerdrLaunchCurrent(
	ctx context.Context,
	homeDir string,
	operationID string,
) (canonicalV19HerdrLaunchCurrent, error) {
	db, err := openReadOnly(homeDir)
	if err != nil {
		return canonicalV19HerdrLaunchCurrent{}, err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19HerdrLaunchCurrent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := loadCanonicalV19LaunchCurrent(ctx, tx, operationID)
	if err != nil {
		return canonicalV19HerdrLaunchCurrent{}, err
	}
	result := canonicalV19HerdrLaunchCurrent{Current: current}
	if err := tx.QueryRowContext(ctx, `SELECT p.fleet_id,b.path,s.provider_session_key
		FROM project p
		JOIN session_binding s ON s.id=? AND s.attempt_id=?
		JOIN attempt_worktree_binding b ON b.id=? AND b.id=s.worktree_binding_id
		WHERE p.id=?`, current.Request.SessionBindingID, current.Request.AttemptID,
		current.Request.WorktreeBindingID, current.Request.ProjectID).Scan(
		&result.FleetID, &result.WorktreePath, &result.ProviderSessionKey,
	); err != nil {
		return canonicalV19HerdrLaunchCurrent{}, fmt.Errorf("read canonical v19 Herdr Launch provider context: %w", err)
	}
	if result.FleetID == "" || result.WorktreePath == "" || result.ProviderSessionKey == "" {
		return canonicalV19HerdrLaunchCurrent{}, fmt.Errorf("read canonical v19 Herdr Launch provider context: Fleet ID, Worktree path, or provider Session key is empty")
	}
	return result, nil
}

func readCanonicalV19HerdrLaunchTerminal(ctx context.Context, homeDir, operationID string) (string, bool, error) {
	db, err := openReadOnly(homeDir)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = db.Close() }()
	var state string
	err = db.sql.QueryRowContext(ctx, `SELECT state FROM external_operation
		WHERE id=? AND kind='launch' AND state IN ('succeeded','rejected','no-effect')`, operationID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return state, true, nil
}

func finalizeCanonicalV19HerdrLaunchObservation(
	current canonicalV19HerdrLaunchCurrent,
	observed canonicalV19HerdrLaunchObservation,
) canonicalV19HerdrLaunchObservation {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:herdr-launch-observation:v1")
	writeCanonicalV19DigestField(hash, "operation_id", current.Current.Request.OperationID)
	writeCanonicalV19DigestField(hash, "request_digest", current.Current.Request.RequestDigest)
	writeCanonicalV19DigestField(hash, "fleet_id", current.FleetID)
	writeCanonicalV19DigestField(hash, "provider_session_key", current.ProviderSessionKey)
	writeCanonicalV19DigestField(hash, "state", string(observed.State))
	writeCanonicalV19DigestField(hash, "workspace_id", observed.WorkspaceID)
	writeCanonicalV19DigestField(hash, "tab_id", observed.TabID)
	writeCanonicalV19DigestField(hash, "pane_id", observed.PaneID)
	writeCanonicalV19DigestField(hash, "pane_cwd", observed.PaneCwd)
	writeCanonicalV19DigestField(hash, "shell_pid", strconv.Itoa(observed.ShellPID))
	writeCanonicalV19DigestField(hash, "process_group_id", strconv.Itoa(observed.ProcessGroupID))
	writeCanonicalV19DigestField(hash, "process_id", strconv.Itoa(observed.ProcessID))
	writeCanonicalV19DigestField(hash, "process_digest", observed.ProcessDigest)
	writeCanonicalV19DigestField(hash, "provider_executor_key", observed.ProviderExecutorKey)
	writeCanonicalV19DigestField(hash, "reason", observed.Reason)
	observed.EvidenceDigest = hex.EncodeToString(hash.Sum(nil))
	return observed
}

func canonicalV19HerdrLaunchJoinReason(left, right string) string {
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	return left + "; " + right
}
