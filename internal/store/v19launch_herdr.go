package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	handgit "github.com/atqamz/hand/internal/git"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/launch"
)

type canonicalV19HerdrLaunchObservationState string

const (
	canonicalV19HerdrLaunchIdle     canonicalV19HerdrLaunchObservationState = "idle"
	canonicalV19HerdrLaunchExact    canonicalV19HerdrLaunchObservationState = "exact"
	canonicalV19HerdrLaunchMismatch canonicalV19HerdrLaunchObservationState = "mismatch"
	canonicalV19HerdrLaunchUnknown  canonicalV19HerdrLaunchObservationState = "unknown"
)

type canonicalV19HerdrExecutorKey struct {
	SessionName    string
	WorkspaceID    string
	TabID          string
	PaneID         string
	ProcessGroupID int
	ProcessID      int
}

type canonicalV19HerdrLaunchObservation struct {
	State               canonicalV19HerdrLaunchObservationState
	ProviderSessionKey  string
	ProviderExecutorKey string
	WorkspaceID         string
	TabID               string
	PaneID              string
	ProcessGroupID      int
	ProcessID           int
	ProcessCwd          string
	EvidenceDigest      string
	Reason              string
}

type canonicalV19HerdrLaunchCurrent struct {
	Current            canonicalV19LaunchCurrent
	FleetID            string
	ProviderSessionKey string
}

type canonicalV19HerdrLaunchClient interface {
	ObserveSession(context.Context) herdr.SessionObservation
	WorkspaceListContext(context.Context) ([]herdr.Workspace, error)
	TabList(string) ([]herdr.Tab, error)
	PaneGetContext(context.Context, string) (herdr.Pane, error)
	PaneProcessInfo(string) (herdr.ProcessInfo, error)
	PaneRunCanonicalSpec(string, launch.LaunchSpec) error
}

type canonicalV19HerdrLaunchDeps struct {
	clientFor func(string) canonicalV19HerdrLaunchClient
	now       func() time.Time
}

// ReconcileCanonicalV19HerdrLaunch reconciles one exact canonical v19 Launch
// against Herdr without replaying submitted or uncertain provider mutation.
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
	switch current.Current.State {
	case "succeeded", "rejected", "no-effect":
		return current.Current.State, nil
	case "prepared", "submitted", "uncertain":
	default:
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr Launch: %w: operation %q is %q",
			ErrCanonicalV19LaunchTransition, operationID, current.Current.State)
	}
	request := current.Current.Request
	if request.AdapterRef != canonicalV19HerdrSessionAdapterRef {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr Launch: %w: adapter %q is not %q",
			ErrCanonicalV19LaunchNotCurrent, request.AdapterRef, canonicalV19HerdrSessionAdapterRef)
	}
	providerSpec, err := canonicalV19HerdrProviderLaunchSpec(request.Spec)
	if err != nil {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr Launch: %w", err)
	}
	sessionKey, err := parseCanonicalV19HerdrSessionProviderKey(current.ProviderSessionKey)
	if err != nil {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr Launch: %w: invalid provider Session key: %v",
			ErrCanonicalV19LaunchNotCurrent, err)
	}
	expectedSession := herdr.SessionName(current.FleetID)
	if sessionKey.SessionName != expectedSession {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr Launch: %w: provider Session key names %q, want %q",
			ErrCanonicalV19LaunchNotCurrent, sessionKey.SessionName, expectedSession)
	}
	client := deps.clientFor(expectedSession)
	if client == nil {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr Launch: provider client is unavailable")
	}

	observed := observeCanonicalV19HerdrLaunch(ctx, current, sessionKey, client)
	if current.Current.State != "prepared" {
		return reconcileCanonicalV19SubmittedHerdrLaunch(ctx, homeDir, current, observed, deps.now)
	}
	switch observed.State {
	case canonicalV19HerdrLaunchIdle:
	case canonicalV19HerdrLaunchExact:
		return "prepared", fmt.Errorf("reconcile canonical v19 Herdr Launch: requested executor already exists before provider mutation was authorized")
	case canonicalV19HerdrLaunchMismatch:
		return "prepared", fmt.Errorf("reconcile canonical v19 Herdr Launch: exact Session/Executor ownership is unresolved: %s", observed.Reason)
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

	observed = observeCanonicalV19HerdrLaunch(ctx, current, sessionKey, client)
	switch observed.State {
	case canonicalV19HerdrLaunchExact:
		return establishCanonicalV19ObservedHerdrExecutor(ctx, homeDir, current, observed, deps.now)
	case canonicalV19HerdrLaunchIdle:
	case canonicalV19HerdrLaunchMismatch, canonicalV19HerdrLaunchUnknown:
		return classifyCanonicalV19HerdrLaunchUncertain(ctx, homeDir, current, observed, deps.now, nil)
	default:
		return "submitted", fmt.Errorf("reconcile canonical v19 Herdr Launch: provider observation state %q is invalid", observed.State)
	}

	performErr := client.PaneRunCanonicalSpec(sessionKey.PaneID, providerSpec)
	observed = observeCanonicalV19HerdrLaunch(ctx, current, sessionKey, client)
	switch observed.State {
	case canonicalV19HerdrLaunchExact:
		return establishCanonicalV19ObservedHerdrExecutor(ctx, homeDir, current, observed, deps.now)
	case canonicalV19HerdrLaunchIdle:
		if performErr != nil && herdr.IsProcessNotStarted(performErr) {
			return classifyCanonicalV19HerdrLaunchNoEffect(ctx, homeDir, current, observed, deps.now,
				"Herdr process did not start and the exact Session pane remains idle")
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
) (string, error) {
	switch observed.State {
	case canonicalV19HerdrLaunchExact:
		return establishCanonicalV19ObservedHerdrExecutor(ctx, homeDir, current, observed, now)
	case canonicalV19HerdrLaunchIdle, canonicalV19HerdrLaunchMismatch, canonicalV19HerdrLaunchUnknown:
		if observed.Reason == "" {
			observed.Reason = "submitted Launch cannot be replayed without positive exact executor evidence"
		} else {
			observed.Reason += "; submitted Launch cannot be replayed without positive exact executor evidence"
		}
		observed = finalizeCanonicalV19HerdrLaunchObservation(current, observed)
		if current.Current.State == "submitted" {
			return classifyCanonicalV19HerdrLaunchUncertain(ctx, homeDir, current, observed, now, nil)
		}
		return "uncertain", fmt.Errorf("reconcile canonical v19 Herdr Launch: operation remains uncertain: %s", observed.Reason)
	default:
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr Launch: provider observation state %q is invalid", observed.State)
	}
}

func establishCanonicalV19ObservedHerdrExecutor(
	ctx context.Context,
	homeDir string,
	current canonicalV19HerdrLaunchCurrent,
	observed canonicalV19HerdrLaunchObservation,
	now func() time.Time,
) (string, error) {
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
		OperationID: current.Current.Request.OperationID, State: "no-effect",
		ObservedAt: observedAt, EvidenceDigest: observed.EvidenceDigest,
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
		return "uncertain", fmt.Errorf("reconcile canonical v19 Herdr Launch: operation remains uncertain: %s", observed.Reason)
	}
	observedAt := canonicalV19HerdrSessionTimestampAfter(now(), current.Current.StateChangedAt)
	if err := ClassifyCanonicalV19Launch(ctx, homeDir, CanonicalV19LaunchTransitionInput{
		OperationID: current.Current.Request.OperationID, State: "uncertain",
		ObservedAt: observedAt, EvidenceDigest: observed.EvidenceDigest,
	}); err != nil {
		return current.Current.State, err
	}
	reason := observed.Reason
	if performErr != nil {
		if reason != "" {
			reason += "; "
		}
		reason += "Herdr Launch failed: " + canonicalV19HerdrSessionErrorText(performErr)
	}
	if reason == "" {
		reason = "strongest provider evidence cannot classify the submitted Launch"
	}
	return "uncertain", fmt.Errorf("reconcile canonical v19 Herdr Launch: operation is uncertain: %s", reason)
}

func canonicalV19HerdrProviderLaunchSpec(spec CanonicalV19LaunchSpec) (launch.LaunchSpec, error) {
	environment := make(map[string]string, len(spec.Environment))
	for name, value := range spec.Environment {
		switch value.ValueKind {
		case "literal":
			environment[name] = value.ValueMaterial
		case "secret-ref":
			return launch.LaunchSpec{}, fmt.Errorf("secret environment reference %q has no canonical runtime resolver", name)
		default:
			return launch.LaunchSpec{}, fmt.Errorf("environment %q has unsupported value kind %q", name, value.ValueKind)
		}
	}
	return launch.NewSpec(launch.LaunchSpec{
		Executable: spec.Executable,
		Args:       append([]string(nil), spec.Arguments...),
		Env:        environment,
		Cwd:        spec.Cwd,
	})
}

func observeCanonicalV19HerdrLaunch(
	ctx context.Context,
	current canonicalV19HerdrLaunchCurrent,
	sessionKey canonicalV19HerdrSessionProviderKey,
	client canonicalV19HerdrLaunchClient,
) canonicalV19HerdrLaunchObservation {
	request := current.Current.Request
	observation := canonicalV19HerdrLaunchObservation{
		ProviderSessionKey: current.ProviderSessionKey,
		WorkspaceID: sessionKey.WorkspaceID, TabID: sessionKey.TabID, PaneID: sessionKey.PaneID,
	}
	expectedSession := herdr.SessionName(current.FleetID)
	session := client.ObserveSession(ctx)
	if session.Name != expectedSession || session.State != herdr.SessionRunningCompatible {
		observation.State = canonicalV19HerdrLaunchUnknown
		observation.Reason = fmt.Sprintf("exact Herdr session %q is %q", expectedSession, session.State)
		if session.Reason != "" {
			observation.Reason += ": " + session.Reason
		}
		return finalizeCanonicalV19HerdrLaunchObservation(current, observation)
	}
	workspaces, err := client.WorkspaceListContext(ctx)
	if err != nil {
		observation.State = canonicalV19HerdrLaunchUnknown
		observation.Reason = "list exact Herdr session workspaces: " + canonicalV19HerdrSessionErrorText(err)
		return finalizeCanonicalV19HerdrLaunchObservation(current, observation)
	}
	workspaceMatches := 0
	for _, workspace := range workspaces {
		if workspace.WorkspaceID != sessionKey.WorkspaceID {
			continue
		}
		workspaceMatches++
		if workspace.Label != canonicalV19HerdrSessionWorkspaceLabel(request.SessionBindingID) {
			observation.State = canonicalV19HerdrLaunchMismatch
			observation.Reason = "exact Session workspace no longer carries its dedicated binding locator"
			return finalizeCanonicalV19HerdrLaunchObservation(current, observation)
		}
	}
	if workspaceMatches != 1 {
		observation.State = canonicalV19HerdrLaunchMismatch
		observation.Reason = fmt.Sprintf("exact Session workspace identity occurs %d times, want 1", workspaceMatches)
		return finalizeCanonicalV19HerdrLaunchObservation(current, observation)
	}
	tabs, err := client.TabList(sessionKey.WorkspaceID)
	if err != nil {
		observation.State = canonicalV19HerdrLaunchUnknown
		observation.Reason = "list exact Session workspace tabs: " + canonicalV19HerdrSessionErrorText(err)
		return finalizeCanonicalV19HerdrLaunchObservation(current, observation)
	}
	if len(tabs) != 1 || tabs[0].TabID != sessionKey.TabID || tabs[0].WorkspaceID != sessionKey.WorkspaceID {
		observation.State = canonicalV19HerdrLaunchMismatch
		observation.Reason = "dedicated Session workspace root tab identity differs from the persisted provider Session key"
		return finalizeCanonicalV19HerdrLaunchObservation(current, observation)
	}
	pane, err := client.PaneGetContext(ctx, sessionKey.PaneID)
	if err != nil {
		if errors.Is(err, herdr.ErrNotFound) {
			observation.State = canonicalV19HerdrLaunchMismatch
			observation.Reason = "exact Session root pane is absent"
		} else {
			observation.State = canonicalV19HerdrLaunchUnknown
			observation.Reason = "observe exact Session root pane: " + canonicalV19HerdrSessionErrorText(err)
		}
		return finalizeCanonicalV19HerdrLaunchObservation(current, observation)
	}
	if pane.PaneID != sessionKey.PaneID || pane.TabID != sessionKey.TabID || pane.WorkspaceID != sessionKey.WorkspaceID {
		observation.State = canonicalV19HerdrLaunchMismatch
		observation.Reason = "exact Session root pane parent identities differ from the persisted provider Session key"
		return finalizeCanonicalV19HerdrLaunchObservation(current, observation)
	}
	if pane.Cwd == "" || !handgit.SamePath(pane.Cwd, request.Spec.Cwd) {
		observation.State = canonicalV19HerdrLaunchMismatch
		observation.Reason = "exact Session root pane cwd differs from the persisted Launch cwd"
		return finalizeCanonicalV19HerdrLaunchObservation(current, observation)
	}
	info, err := client.PaneProcessInfo(sessionKey.PaneID)
	if err != nil {
		observation.State = canonicalV19HerdrLaunchUnknown
		observation.Reason = "observe exact Session pane process info: " + canonicalV19HerdrSessionErrorText(err)
		return finalizeCanonicalV19HerdrLaunchObservation(current, observation)
	}
	if info.PaneID != "" && info.PaneID != sessionKey.PaneID {
		observation.State = canonicalV19HerdrLaunchMismatch
		observation.Reason = "process observation names a different pane"
		return finalizeCanonicalV19HerdrLaunchObservation(current, observation)
	}
	if canonicalV19HerdrLaunchProcessInfoIdle(info, request.Spec.Cwd) {
		observation.State = canonicalV19HerdrLaunchIdle
		return finalizeCanonicalV19HerdrLaunchObservation(current, observation)
	}
	process, matches := canonicalV19HerdrLaunchMatchingProcesses(info, request.Spec)
	if matches == 1 && info.ForegroundProcessGroupID > 0 && info.ForegroundProcessGroupID != info.ShellPID {
		observation.State = canonicalV19HerdrLaunchExact
		observation.ProcessGroupID = info.ForegroundProcessGroupID
		observation.ProcessID = process.PID
		observation.ProcessCwd = process.Cwd
		key, keyErr := encodeCanonicalV19HerdrExecutorKey(canonicalV19HerdrExecutorKey{
			SessionName: expectedSession, WorkspaceID: sessionKey.WorkspaceID,
			TabID: sessionKey.TabID, PaneID: sessionKey.PaneID,
			ProcessGroupID: info.ForegroundProcessGroupID, ProcessID: process.PID,
		})
		if keyErr != nil {
			observation.State = canonicalV19HerdrLaunchUnknown
			observation.Reason = "encode exact provider Executor key: " + keyErr.Error()
			return finalizeCanonicalV19HerdrLaunchObservation(current, observation)
		}
		observation.ProviderExecutorKey = key
		return finalizeCanonicalV19HerdrLaunchObservation(current, observation)
	}
	observation.State = canonicalV19HerdrLaunchMismatch
	if matches > 1 {
		observation.Reason = "provider reports the exact requested executable/argv/cwd more than once in the foreground process group"
	} else if matches == 1 {
		observation.Reason = "requested executor is visible but foreground process-group identity is not a distinct execution"
	} else {
		observation.Reason = "Session pane is neither exact idle shell nor the requested executor"
	}
	return finalizeCanonicalV19HerdrLaunchObservation(current, observation)
}

func canonicalV19HerdrLaunchProcessInfoIdle(info herdr.ProcessInfo, cwd string) bool {
	if info.ShellPID <= 0 || info.ForegroundProcessGroupID != info.ShellPID || len(info.ForegroundProcesses) != 1 {
		return false
	}
	process := info.ForegroundProcesses[0]
	if process.PID != info.ShellPID || process.Cwd == "" || !handgit.SamePath(process.Cwd, cwd) {
		return false
	}
	switch canonicalV19HerdrExecutableBase(canonicalV19HerdrProcessExecutable(process)) {
	case "sh", "bash", "zsh", "powershell", "pwsh":
		return true
	default:
		return false
	}
}

func canonicalV19HerdrLaunchMatchingProcesses(info herdr.ProcessInfo, spec CanonicalV19LaunchSpec) (herdr.Process, int) {
	var match herdr.Process
	matches := 0
	for _, process := range info.ForegroundProcesses {
		if process.PID <= 0 || process.PID == info.ShellPID || process.Cwd == "" || !handgit.SamePath(process.Cwd, spec.Cwd) {
			continue
		}
		if len(process.Argv) != len(spec.Arguments)+1 {
			continue
		}
		if canonicalV19HerdrExecutableBase(process.Argv[0]) != canonicalV19HerdrExecutableBase(spec.Executable) {
			continue
		}
		exactArgs := true
		for i, expected := range spec.Arguments {
			if process.Argv[i+1] != expected {
				exactArgs = false
				break
			}
		}
		if !exactArgs {
			continue
		}
		match = process
		matches++
	}
	return match, matches
}

func canonicalV19HerdrProcessExecutable(process herdr.Process) string {
	if process.Name != "" {
		return process.Name
	}
	return process.Argv0
}

func canonicalV19HerdrExecutableBase(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
	value = strings.TrimPrefix(path.Base(value), "-")
	value = strings.ToLower(value)
	return strings.TrimSuffix(value, ".exe")
}

func encodeCanonicalV19HerdrExecutorKey(key canonicalV19HerdrExecutorKey) (string, error) {
	for name, value := range map[string]string{
		"session": key.SessionName, "workspace": key.WorkspaceID, "tab": key.TabID, "pane": key.PaneID,
	} {
		if value == "" {
			return "", fmt.Errorf("Herdr provider Executor key %s is empty", name)
		}
	}
	if key.ProcessGroupID <= 0 || key.ProcessID <= 0 {
		return "", fmt.Errorf("Herdr provider Executor process identities must be positive")
	}
	values := url.Values{}
	values.Set("pane", key.PaneID)
	values.Set("pgid", strconv.Itoa(key.ProcessGroupID))
	values.Set("pid", strconv.Itoa(key.ProcessID))
	values.Set("session", key.SessionName)
	values.Set("tab", key.TabID)
	values.Set("workspace", key.WorkspaceID)
	return "herdr-executor:v1?" + values.Encode(), nil
}

func parseCanonicalV19HerdrExecutorKey(value string) (canonicalV19HerdrExecutorKey, error) {
	const prefix = "herdr-executor:v1?"
	if !strings.HasPrefix(value, prefix) {
		return canonicalV19HerdrExecutorKey{}, fmt.Errorf("missing %q prefix", prefix)
	}
	values, err := url.ParseQuery(strings.TrimPrefix(value, prefix))
	if err != nil {
		return canonicalV19HerdrExecutorKey{}, fmt.Errorf("parse query: %w", err)
	}
	if len(values) != 6 {
		return canonicalV19HerdrExecutorKey{}, fmt.Errorf("want exactly session/workspace/tab/pane/pgid/pid fields")
	}
	key := canonicalV19HerdrExecutorKey{
		SessionName: values.Get("session"), WorkspaceID: values.Get("workspace"),
		TabID: values.Get("tab"), PaneID: values.Get("pane"),
	}
	for name, raw := range map[string]string{"pgid": values.Get("pgid"), "pid": values.Get("pid")} {
		parsed, parseErr := strconv.Atoi(raw)
		if parseErr != nil || parsed <= 0 {
			return canonicalV19HerdrExecutorKey{}, fmt.Errorf("%s is not a positive integer", name)
		}
		if name == "pgid" {
			key.ProcessGroupID = parsed
		} else {
			key.ProcessID = parsed
		}
	}
	for name, value := range map[string]string{
		"session": key.SessionName, "workspace": key.WorkspaceID, "tab": key.TabID, "pane": key.PaneID,
	} {
		if value == "" {
			return canonicalV19HerdrExecutorKey{}, fmt.Errorf("%s is empty", name)
		}
	}
	return key, nil
}

func readCanonicalV19HerdrLaunchCurrent(ctx context.Context, homeDir, operationID string) (canonicalV19HerdrLaunchCurrent, error) {
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
	err = tx.QueryRowContext(ctx, `SELECT p.fleet_id,s.provider_session_key
		FROM project p
		JOIN session_binding s ON s.id=? AND s.attempt_id=? AND s.adapter_ref=?
		WHERE p.id=?
		  AND NOT EXISTS (SELECT 1 FROM session_binding_release r WHERE r.session_binding_id=s.id)`,
		current.Request.SessionBindingID, current.Request.AttemptID, current.Request.AdapterRef, current.Request.ProjectID,
	).Scan(&result.FleetID, &result.ProviderSessionKey)
	if errors.Is(err, sql.ErrNoRows) {
		return canonicalV19HerdrLaunchCurrent{}, fmt.Errorf("%w: operation %q lacks exact open provider SessionBinding", ErrCanonicalV19LaunchNotCurrent, operationID)
	}
	if err != nil {
		return canonicalV19HerdrLaunchCurrent{}, fmt.Errorf("read canonical v19 Herdr Launch provider context: %w", err)
	}
	if result.FleetID == "" || result.ProviderSessionKey == "" {
		return canonicalV19HerdrLaunchCurrent{}, fmt.Errorf("%w: operation %q provider context is incomplete", ErrCanonicalV19LaunchNotCurrent, operationID)
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
	err = db.sql.QueryRowContext(ctx, `SELECT o.state
		FROM external_operation o
		JOIN launch_operation l ON l.operation_id=o.id
		WHERE o.id=? AND o.kind='launch' AND o.adapter_ref=?
		  AND o.state IN ('succeeded','rejected','no-effect')
		  AND (o.state<>'succeeded' OR EXISTS (
			SELECT 1 FROM executor_binding e WHERE e.launch_operation_id=o.id AND e.id=l.binding_id
		  ))`, operationID, canonicalV19HerdrSessionAdapterRef).Scan(&state)
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
	writeCanonicalV19DigestField(hash, "provider_session_key", observed.ProviderSessionKey)
	writeCanonicalV19DigestField(hash, "state", string(observed.State))
	writeCanonicalV19DigestField(hash, "workspace_id", observed.WorkspaceID)
	writeCanonicalV19DigestField(hash, "tab_id", observed.TabID)
	writeCanonicalV19DigestField(hash, "pane_id", observed.PaneID)
	writeCanonicalV19DigestField(hash, "process_group_id", strconv.Itoa(observed.ProcessGroupID))
	writeCanonicalV19DigestField(hash, "process_id", strconv.Itoa(observed.ProcessID))
	writeCanonicalV19DigestField(hash, "process_cwd", observed.ProcessCwd)
	writeCanonicalV19DigestField(hash, "provider_executor_key", observed.ProviderExecutorKey)
	writeCanonicalV19DigestField(hash, "reason", observed.Reason)
	observed.EvidenceDigest = hex.EncodeToString(hash.Sum(nil))
	return observed
}
