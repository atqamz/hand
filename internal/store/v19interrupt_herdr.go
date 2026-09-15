package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	handgit "github.com/atqamz/hand/internal/git"
	"github.com/atqamz/hand/internal/herdr"
)

type canonicalV19HerdrInterruptObservationState string

const (
	canonicalV19HerdrInterruptRunning  canonicalV19HerdrInterruptObservationState = "running"
	canonicalV19HerdrInterruptCeased   canonicalV19HerdrInterruptObservationState = "ceased"
	canonicalV19HerdrInterruptMismatch canonicalV19HerdrInterruptObservationState = "mismatch"
	canonicalV19HerdrInterruptUnknown  canonicalV19HerdrInterruptObservationState = "unknown"
)

type canonicalV19HerdrInterruptObservation struct {
	State          canonicalV19HerdrInterruptObservationState
	ProcessAlive   bool
	WorkspaceID    string
	TabID          string
	PaneID         string
	PaneCwd        string
	ShellPID       int
	ProcessGroupID int
	ProcessID      int
	ProcessDigest  string
	EvidenceDigest string
	Reason         string
}

type canonicalV19HerdrInterruptCurrent struct {
	Current            canonicalV19InterruptCurrent
	FleetID            string
	WorktreePath       string
	ProviderSessionKey string
}

type canonicalV19HerdrInterruptClient interface {
	ObserveSession(context.Context) herdr.SessionObservation
	WorkspaceListContext(context.Context) ([]herdr.Workspace, error)
	TabList(string) ([]herdr.Tab, error)
	PaneGetContext(context.Context, string) (herdr.Pane, error)
	PaneProcessInfo(string) (herdr.ProcessInfo, error)
	PaneSendKeys(string, ...string) error
}

type canonicalV19HerdrInterruptDeps struct {
	clientFor    func(string) canonicalV19HerdrInterruptClient
	processAlive func(int) (bool, error)
	now          func() time.Time
}

// ReconcileCanonicalV19HerdrInterrupt reconciles one exact canonical v19 Interrupt.
// The selected managed provider is refused before submission until its ExecutorBinding
// proves exact execution identity and positive cessation.
func ReconcileCanonicalV19HerdrInterrupt(ctx context.Context, homeDir, operationID string) (string, error) {
	return reconcileCanonicalV19HerdrInterrupt(ctx, homeDir, operationID, canonicalV19HerdrInterruptDeps{
		clientFor: func(sessionName string) canonicalV19HerdrInterruptClient {
			return herdr.NewManagedSessionClient(sessionName)
		},
		processAlive: canonicalV19HerdrProcessAlive,
		now:          time.Now,
	})
}

func reconcileCanonicalV19HerdrInterrupt(
	ctx context.Context,
	homeDir string,
	operationID string,
	deps canonicalV19HerdrInterruptDeps,
) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if operationID == "" {
		return "", fmt.Errorf("reconcile canonical v19 Herdr Interrupt: operation ID is empty")
	}
	if deps.clientFor == nil || deps.processAlive == nil || deps.now == nil {
		return "", fmt.Errorf("reconcile canonical v19 Herdr Interrupt: adapter dependencies are incomplete")
	}

	current, err := readCanonicalV19HerdrInterruptCurrent(ctx, homeDir, operationID)
	if err != nil {
		if errors.Is(err, ErrCanonicalV19InterruptNotCurrent) {
			if state, found, terminalErr := readCanonicalV19HerdrInterruptTerminal(ctx, homeDir, operationID); terminalErr != nil {
				return "", fmt.Errorf("reconcile canonical v19 Herdr Interrupt: %w", terminalErr)
			} else if found {
				return state, nil
			}
		}
		return "", fmt.Errorf("reconcile canonical v19 Herdr Interrupt: %w", err)
	}
	request := current.Current.Request
	switch current.Current.State {
	case "succeeded", "rejected", "no-effect":
		return current.Current.State, nil
	case "prepared", "submitted", "uncertain":
	default:
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr Interrupt: %w: operation %q is %q",
			ErrCanonicalV19InterruptTransition, operationID, current.Current.State)
	}
	if request.AdapterRef != canonicalV19HerdrSessionAdapterRef {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr Interrupt: %w: adapter %q is not %q",
			ErrCanonicalV19InterruptNotCurrent, request.AdapterRef, canonicalV19HerdrSessionAdapterRef)
	}
	unsupportedErr := canonicalV19HerdrCapabilityUnsupported(
		"Interrupt", "exact execution identity and positive cessation evidence",
	)
	if current.Current.State == "uncertain" {
		return current.Current.State, unsupportedErr
	}
	state := "uncertain"
	if current.Current.State == "prepared" {
		state = "no-effect"
	}
	if err := ClassifyCanonicalV19Interrupt(ctx, homeDir, CanonicalV19InterruptTransitionInput{
		OperationID:    operationID,
		State:          state,
		ObservedAt:     canonicalV19HerdrSessionTimestampAfter(deps.now(), current.Current.StateChangedAt),
		EvidenceDigest: canonicalV19HerdrUnsupportedEvidenceDigest("Interrupt", operationID, request.RequestDigest, state),
	}); err != nil {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr Interrupt: persist unsupported %s transition: %w", state, err)
	}
	return state, unsupportedErr
}

func observeCanonicalV19HerdrInterrupt(
	ctx context.Context,
	current canonicalV19HerdrInterruptCurrent,
	executorKey canonicalV19HerdrExecutorProviderKey,
	sessionKey canonicalV19HerdrSessionProviderKey,
	client canonicalV19HerdrInterruptClient,
	processAlive func(int) (bool, error),
) canonicalV19HerdrInterruptObservation {
	request := current.Current.Request
	observed := canonicalV19HerdrInterruptObservation{
		WorkspaceID: executorKey.WorkspaceID,
		TabID:       executorKey.TabID,
		PaneID:      executorKey.PaneID,
		ProcessID:   executorKey.ProcessID,
	}

	alive, aliveErr := processAlive(executorKey.ProcessID)
	observed.ProcessAlive = alive
	if aliveErr == nil && !alive {
		observed.State = canonicalV19HerdrInterruptUnknown
		observed.Reason = "provider Executor PID is absent, but Herdr v0.8.2 cannot prove exact execution cessation"
		return finalizeCanonicalV19HerdrInterruptObservation(current, observed)
	}

	session := client.ObserveSession(ctx)
	if session.Name != executorKey.SessionName || session.State != herdr.SessionRunningCompatible {
		observed.State = canonicalV19HerdrInterruptUnknown
		observed.Reason = fmt.Sprintf("exact Herdr session %q is %q", executorKey.SessionName, session.State)
		if session.Reason != "" {
			observed.Reason += ": " + session.Reason
		}
		if aliveErr != nil {
			observed.Reason = canonicalV19HerdrLaunchJoinReason(observed.Reason, "process liveness probe failed: "+aliveErr.Error())
		}
		return finalizeCanonicalV19HerdrInterruptObservation(current, observed)
	}

	workspaces, err := client.WorkspaceListContext(ctx)
	if err != nil {
		observed.State = canonicalV19HerdrInterruptUnknown
		observed.Reason = "list exact Herdr session workspaces: " + canonicalV19HerdrSessionErrorText(err)
		return finalizeCanonicalV19HerdrInterruptObservation(current, observed)
	}
	workspaceMatches := 0
	var workspace herdr.Workspace
	for _, candidate := range workspaces {
		if candidate.WorkspaceID == sessionKey.WorkspaceID {
			workspace = candidate
			workspaceMatches++
		}
	}
	if workspaceMatches != 1 {
		observed.State = canonicalV19HerdrInterruptMismatch
		if workspaceMatches == 0 {
			observed.Reason = "exact Session workspace is absent while the provider Executor PID remains live"
		} else {
			observed.Reason = "Herdr workspace inventory returned the exact Session workspace more than once"
		}
		return finalizeCanonicalV19HerdrInterruptObservation(current, observed)
	}
	if workspace.Label != canonicalV19HerdrSessionWorkspaceLabel(request.SessionBindingID) {
		observed.State = canonicalV19HerdrInterruptMismatch
		observed.Reason = "exact workspace identity no longer carries the SessionBinding locator"
		return finalizeCanonicalV19HerdrInterruptObservation(current, observed)
	}

	tabs, err := client.TabList(sessionKey.WorkspaceID)
	if err != nil {
		observed.State = canonicalV19HerdrInterruptUnknown
		observed.Reason = "list exact Session workspace tabs: " + canonicalV19HerdrSessionErrorText(err)
		return finalizeCanonicalV19HerdrInterruptObservation(current, observed)
	}
	if len(tabs) != 1 || tabs[0].TabID != sessionKey.TabID || tabs[0].WorkspaceID != sessionKey.WorkspaceID {
		observed.State = canonicalV19HerdrInterruptMismatch
		observed.Reason = "Session workspace root tab no longer matches the persisted provider Session key"
		return finalizeCanonicalV19HerdrInterruptObservation(current, observed)
	}

	pane, err := client.PaneGetContext(ctx, sessionKey.PaneID)
	if err != nil {
		if errors.Is(err, herdr.ErrNotFound) {
			observed.State = canonicalV19HerdrInterruptMismatch
			observed.Reason = "exact Session root pane is absent while the provider Executor PID remains live"
		} else {
			observed.State = canonicalV19HerdrInterruptUnknown
			observed.Reason = "observe exact Session root pane: " + canonicalV19HerdrSessionErrorText(err)
		}
		return finalizeCanonicalV19HerdrInterruptObservation(current, observed)
	}
	observed.PaneCwd = pane.Cwd
	if pane.PaneID != sessionKey.PaneID || pane.TabID != sessionKey.TabID || pane.WorkspaceID != sessionKey.WorkspaceID {
		observed.State = canonicalV19HerdrInterruptMismatch
		observed.Reason = "exact root pane parent identities differ from the provider Session key"
		return finalizeCanonicalV19HerdrInterruptObservation(current, observed)
	}
	if pane.Cwd == "" || !handgit.SamePath(pane.Cwd, current.WorktreePath) {
		observed.State = canonicalV19HerdrInterruptMismatch
		observed.Reason = "exact root pane cwd differs from the immutable WorktreeBinding"
		return finalizeCanonicalV19HerdrInterruptObservation(current, observed)
	}

	info, err := client.PaneProcessInfo(sessionKey.PaneID)
	if err != nil {
		observed.State = canonicalV19HerdrInterruptUnknown
		observed.Reason = "observe exact pane process identity: " + canonicalV19HerdrSessionErrorText(err)
		return finalizeCanonicalV19HerdrInterruptObservation(current, observed)
	}
	observed.ShellPID = info.ShellPID
	observed.ProcessGroupID = info.ForegroundProcessGroupID
	if info.PaneID != sessionKey.PaneID || info.ShellPID <= 0 || info.ForegroundProcessGroupID <= 0 {
		observed.State = canonicalV19HerdrInterruptUnknown
		observed.Reason = "pane process evidence lacks exact pane/shell/process-group identity"
		return finalizeCanonicalV19HerdrInterruptObservation(current, observed)
	}

	matches := 0
	for _, process := range info.ForegroundProcesses {
		if process.PID != executorKey.ProcessID {
			continue
		}
		observed.ProcessDigest = canonicalV19HerdrProcessDigest(process)
		if observed.ProcessDigest != executorKey.ProcessDigest {
			observed.State = canonicalV19HerdrInterruptMismatch
			observed.Reason = "provider Executor PID now carries different argv/cwd identity"
			return finalizeCanonicalV19HerdrInterruptObservation(current, observed)
		}
		matches++
	}
	if matches > 1 {
		observed.State = canonicalV19HerdrInterruptMismatch
		observed.Reason = "foreground process evidence returned the exact provider Executor PID more than once"
		return finalizeCanonicalV19HerdrInterruptObservation(current, observed)
	}
	if matches == 1 {
		if info.ForegroundProcessGroupID != executorKey.ProcessGroup {
			observed.State = canonicalV19HerdrInterruptMismatch
			observed.Reason = "exact provider Executor PID is no longer in its persisted foreground process group"
			return finalizeCanonicalV19HerdrInterruptObservation(current, observed)
		}
		observed.State = canonicalV19HerdrInterruptRunning
		return finalizeCanonicalV19HerdrInterruptObservation(current, observed)
	}
	if aliveErr != nil {
		observed.State = canonicalV19HerdrInterruptUnknown
		observed.Reason = "exact provider Executor is absent from foreground evidence and process liveness probe failed: " + aliveErr.Error()
		return finalizeCanonicalV19HerdrInterruptObservation(current, observed)
	}
	if alive {
		observed.State = canonicalV19HerdrInterruptUnknown
		observed.Reason = "exact provider Executor PID remains live but is absent from the exact pane foreground process evidence"
		return finalizeCanonicalV19HerdrInterruptObservation(current, observed)
	}
	observed.State = canonicalV19HerdrInterruptUnknown
	observed.Reason = "provider Executor PID is absent, but Herdr v0.8.2 cannot prove exact execution cessation"
	return finalizeCanonicalV19HerdrInterruptObservation(current, observed)
}

func readCanonicalV19HerdrInterruptCurrent(
	ctx context.Context,
	homeDir string,
	operationID string,
) (canonicalV19HerdrInterruptCurrent, error) {
	db, err := openReadOnly(homeDir)
	if err != nil {
		return canonicalV19HerdrInterruptCurrent{}, err
	}
	defer func() { _ = db.Close() }()
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19HerdrInterruptCurrent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := loadCanonicalV19InterruptCurrent(ctx, tx, operationID)
	if err != nil {
		return canonicalV19HerdrInterruptCurrent{}, err
	}
	result := canonicalV19HerdrInterruptCurrent{Current: current}
	if err := tx.QueryRowContext(ctx, `SELECT p.fleet_id,b.path,s.provider_session_key
		FROM project p
		JOIN session_binding s ON s.id=? AND s.attempt_id=? AND s.adapter_ref=?
		JOIN attempt_worktree_binding b ON b.id=s.worktree_binding_id AND b.attempt_id=s.attempt_id
		WHERE p.id=?`, current.Request.SessionBindingID, current.Request.AttemptID,
		current.Request.AdapterRef, current.Request.ProjectID).Scan(
		&result.FleetID, &result.WorktreePath, &result.ProviderSessionKey,
	); err != nil {
		return canonicalV19HerdrInterruptCurrent{}, fmt.Errorf("read canonical v19 Herdr Interrupt provider context: %w", err)
	}
	if result.FleetID == "" || result.WorktreePath == "" || result.ProviderSessionKey == "" {
		return canonicalV19HerdrInterruptCurrent{}, fmt.Errorf("read canonical v19 Herdr Interrupt provider context: Fleet ID, Worktree path, or provider Session key is empty")
	}
	return result, nil
}

func readCanonicalV19HerdrInterruptTerminal(ctx context.Context, homeDir, operationID string) (string, bool, error) {
	db, err := openReadOnly(homeDir)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = db.Close() }()
	var state string
	err = db.sql.QueryRowContext(ctx, `SELECT state FROM external_operation
		WHERE id=? AND kind='interrupt' AND state IN ('succeeded','rejected','no-effect')`, operationID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return state, true, nil
}

func finalizeCanonicalV19HerdrInterruptObservation(
	current canonicalV19HerdrInterruptCurrent,
	observed canonicalV19HerdrInterruptObservation,
) canonicalV19HerdrInterruptObservation {
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:herdr-interrupt-observation:v1")
	writeCanonicalV19DigestField(hash, "operation_id", current.Current.Request.OperationID)
	writeCanonicalV19DigestField(hash, "request_digest", current.Current.Request.RequestDigest)
	writeCanonicalV19DigestField(hash, "fleet_id", current.FleetID)
	writeCanonicalV19DigestField(hash, "provider_session_key", current.ProviderSessionKey)
	writeCanonicalV19DigestField(hash, "provider_executor_key", current.Current.Request.ProviderExecutorKey)
	writeCanonicalV19DigestField(hash, "state", string(observed.State))
	writeCanonicalV19DigestField(hash, "process_alive", strconv.FormatBool(observed.ProcessAlive))
	writeCanonicalV19DigestField(hash, "workspace_id", observed.WorkspaceID)
	writeCanonicalV19DigestField(hash, "tab_id", observed.TabID)
	writeCanonicalV19DigestField(hash, "pane_id", observed.PaneID)
	writeCanonicalV19DigestField(hash, "pane_cwd", observed.PaneCwd)
	writeCanonicalV19DigestField(hash, "shell_pid", strconv.Itoa(observed.ShellPID))
	writeCanonicalV19DigestField(hash, "process_group_id", strconv.Itoa(observed.ProcessGroupID))
	writeCanonicalV19DigestField(hash, "process_id", strconv.Itoa(observed.ProcessID))
	writeCanonicalV19DigestField(hash, "process_digest", observed.ProcessDigest)
	writeCanonicalV19DigestField(hash, "reason", observed.Reason)
	observed.EvidenceDigest = hex.EncodeToString(hash.Sum(nil))
	return observed
}
