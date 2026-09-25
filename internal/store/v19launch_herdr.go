package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"

	handgit "github.com/atqamz/hand/internal/git"
	"github.com/atqamz/hand/internal/herdr"
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
	PaneRunExecGuard(paneID, cwd, hand, locator string) error
}

type canonicalV19HerdrLaunchDeps struct {
	clientFor func(string) canonicalV19HerdrLaunchClient
	now       func() time.Time
	hand      func() (string, error)
	settle    time.Duration
	// Stays false outside tests until the Linux platform acceptance tests pass (O1, pane tty,
	// real Herdr) and the cessation observer runs for every open guarded binding.
	execGuard bool
}

// ReconcileCanonicalV19HerdrLaunch reconciles one exact canonical v19 Launch against Herdr.
// The selected managed provider is refused before submission until it supplies exact executable
// object and never-reused execution-incarnation identity.
func ReconcileCanonicalV19HerdrLaunch(ctx context.Context, homeDir, operationID string) (string, error) {
	return reconcileCanonicalV19HerdrLaunch(ctx, homeDir, operationID, canonicalV19HerdrLaunchDefaultDeps())
}

func canonicalV19HerdrLaunchDefaultDeps() canonicalV19HerdrLaunchDeps {
	return canonicalV19HerdrLaunchDeps{
		clientFor: func(sessionName string) canonicalV19HerdrLaunchClient {
			return herdr.NewManagedSessionClient(sessionName)
		},
		now:    time.Now,
		hand:   os.Executable,
		settle: 10 * time.Second,
	}
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
				return state, settleCanonicalV19ExecGuardFiles(homeDir, operationID)
			}
		}
		return "", fmt.Errorf("reconcile canonical v19 Herdr Launch: %w", err)
	}
	request := current.Current.Request
	switch current.Current.State {
	case "succeeded", "rejected", "no-effect":
		return current.Current.State, settleCanonicalV19ExecGuardFiles(homeDir, operationID)
	case "prepared", "submitted", "uncertain":
	default:
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr Launch: %w: operation %q is %q",
			ErrCanonicalV19LaunchTransition, operationID, current.Current.State)
	}
	if request.AdapterRef != CanonicalV19HerdrSessionAdapterRef {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr Launch: %w: adapter %q is not %q",
			ErrCanonicalV19LaunchNotCurrent, request.AdapterRef, CanonicalV19HerdrSessionAdapterRef)
	}
	if deps.execGuard {
		if state, handled, err := reconcileCanonicalV19HerdrGuardLaunch(ctx, homeDir, current, deps); handled {
			return state, err
		}
	}
	unsupportedErr := canonicalV19HerdrCapabilityUnsupported(
		"Launch", "exact executable-object and never-reused execution-incarnation identity",
	)
	if current.Current.State == "uncertain" {
		return current.Current.State, unsupportedErr
	}
	if current.Current.State == "prepared" {
		if err := classifyCanonicalV19LaunchPreparedNoEffect(ctx, homeDir, CanonicalV19LaunchTransitionInput{
			OperationID:    operationID,
			State:          "no-effect",
			ObservedAt:     canonicalV19HerdrSessionTimestampAfter(deps.now(), current.Current.StateChangedAt),
			EvidenceDigest: canonicalV19HerdrUnsupportedEvidenceDigest("Launch", operationID, request.RequestDigest, "no-effect"),
		}); err == nil {
			return "no-effect", unsupportedErr
		} else if errors.Is(err, ErrCanonicalV19LaunchTransition) {
			return reconcileCanonicalV19HerdrLaunch(ctx, homeDir, operationID, deps)
		} else {
			return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr Launch: persist unsupported no-effect transition: %w", err)
		}
	}
	state := "uncertain"
	if err := ClassifyCanonicalV19Launch(ctx, homeDir, CanonicalV19LaunchTransitionInput{
		OperationID:    operationID,
		State:          state,
		ObservedAt:     canonicalV19HerdrSessionTimestampAfter(deps.now(), current.Current.StateChangedAt),
		EvidenceDigest: canonicalV19HerdrUnsupportedEvidenceDigest("Launch", operationID, request.RequestDigest, state),
	}); err != nil {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr Launch: persist unsupported %s transition: %w", state, err)
	}
	return state, unsupportedErr
}

func classifyCanonicalV19LaunchPreparedNoEffect(
	ctx context.Context,
	homeDir string,
	input CanonicalV19LaunchTransitionInput,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19LaunchTransitionInput(input); err != nil {
		return err
	}
	if input.State != "no-effect" {
		return fmt.Errorf("classify canonical v19 Launch: prepared settlement requires no-effect state")
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19LaunchWriteError("classify", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("classify canonical v19 Launch: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE external_operation
		SET state='no-effect',state_changed_at=?,state_evidence_digest=?,finalized_at=?
		WHERE id=? AND kind='launch' AND state='prepared'`, input.ObservedAt, input.EvidenceDigest,
		input.ObservedAt, input.OperationID)
	if err != nil {
		return canonicalV19LaunchConstraintError("classify", "settle prepared exact operation", input.OperationID, err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return canonicalV19LaunchWriteError("classify", "count prepared state transition", err)
	}
	if changed != 1 {
		return fmt.Errorf("classify canonical v19 Launch: %w: operation %q was not prepared", ErrCanonicalV19LaunchTransition, input.OperationID)
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19LaunchWriteError("classify", "commit writer", err)
	}
	committed = true
	return nil
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
		observed.State = canonicalV19HerdrLaunchUnknown
		observed.Reason = "Herdr v0.8.2 PID/argv/cwd evidence cannot prove an exact executable object or never-reused execution incarnation"
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
	if !handgit.SamePath(process.Argv[0], spec.Executable) {
		return false
	}
	for i, argument := range spec.Arguments {
		if process.Argv[i+1] != argument {
			return false
		}
	}
	return handgit.SamePath(process.Cwd, spec.Cwd)
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
