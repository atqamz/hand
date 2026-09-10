package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atqamz/hand/internal/herdr"
)

type canonicalV19HerdrWorkerWakeObservationState string

const (
	canonicalV19HerdrWorkerWakeReady    canonicalV19HerdrWorkerWakeObservationState = "ready"
	canonicalV19HerdrWorkerWakeBlocked  canonicalV19HerdrWorkerWakeObservationState = "blocked"
	canonicalV19HerdrWorkerWakeNotReady canonicalV19HerdrWorkerWakeObservationState = "not-ready"
	canonicalV19HerdrWorkerWakeCeased   canonicalV19HerdrWorkerWakeObservationState = "ceased"
	canonicalV19HerdrWorkerWakeMismatch canonicalV19HerdrWorkerWakeObservationState = "mismatch"
	canonicalV19HerdrWorkerWakeUnknown  canonicalV19HerdrWorkerWakeObservationState = "unknown"
)

type canonicalV19HerdrWorkerWakeObservation struct {
	State            canonicalV19HerdrWorkerWakeObservationState
	PendingInput     bool
	ProcessAlive     bool
	ProviderAccepted bool
	Agent            string
	AgentStatus      herdr.Status
	WorkspaceID      string
	TabID            string
	PaneID           string
	PaneCwd          string
	ShellPID         int
	ProcessGroupID   int
	ProcessID        int
	ProcessDigest    string
	EvidenceDigest   string
	Reason           string
}

type canonicalV19HerdrWorkerWakeCurrent struct {
	Current            canonicalV19WorkerWakeCurrent
	FleetID            string
	WorktreePath       string
	ProviderSessionKey string
	PendingInput       bool
}

type canonicalV19HerdrWorkerWakeClient interface {
	ObserveSession(context.Context) herdr.SessionObservation
	WorkspaceListContext(context.Context) ([]herdr.Workspace, error)
	TabList(string) ([]herdr.Tab, error)
	PaneGetContext(context.Context, string) (herdr.Pane, error)
	PaneProcessInfo(string) (herdr.ProcessInfo, error)
	AgentPromptContext(context.Context, string, string) error
}

type canonicalV19HerdrWorkerWakeDeps struct {
	clientFor    func(string) canonicalV19HerdrWorkerWakeClient
	processAlive func(int) (bool, error)
	now          func() time.Time
}

// ReconcileCanonicalV19HerdrWorkerWake reconciles one exact mechanism-only WorkerWake
// against its persisted Herdr ExecutorBinding. Herdr prompt acceptance is mechanism
// success only; this path never creates WorkerInput acknowledgement evidence.
func ReconcileCanonicalV19HerdrWorkerWake(ctx context.Context, homeDir, operationID string) (string, error) {
	return reconcileCanonicalV19HerdrWorkerWake(ctx, homeDir, operationID, canonicalV19HerdrWorkerWakeDeps{
		clientFor: func(sessionName string) canonicalV19HerdrWorkerWakeClient {
			return herdr.NewManagedSessionClient(sessionName)
		},
		processAlive: canonicalV19HerdrProcessAlive,
		now:          time.Now,
	})
}

func reconcileCanonicalV19HerdrWorkerWake(
	ctx context.Context,
	homeDir string,
	operationID string,
	deps canonicalV19HerdrWorkerWakeDeps,
) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if operationID == "" {
		return "", fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: operation ID is empty")
	}
	if deps.clientFor == nil || deps.processAlive == nil || deps.now == nil {
		return "", fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: adapter dependencies are incomplete")
	}

	current, err := readCanonicalV19HerdrWorkerWakeCurrent(ctx, homeDir, operationID)
	if err != nil {
		if errors.Is(err, ErrCanonicalV19WorkerWakeNotCurrent) {
			if state, found, terminalErr := readCanonicalV19HerdrWorkerWakeTerminal(ctx, homeDir, operationID); terminalErr != nil {
				return "", fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: %w", terminalErr)
			} else if found {
				return state, nil
			}
		}
		return "", fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: %w", err)
	}
	request := current.Current.Request
	switch current.Current.State {
	case "succeeded", "rejected", "no-effect":
		return current.Current.State, nil
	case "prepared", "submitted", "uncertain":
	default:
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: %w: operation %q is %q",
			ErrCanonicalV19WorkerWakeTransition, operationID, current.Current.State)
	}
	if request.AdapterRef != canonicalV19HerdrSessionAdapterRef {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: %w: adapter %q is not %q",
			ErrCanonicalV19WorkerWakeNotCurrent, request.AdapterRef, canonicalV19HerdrSessionAdapterRef)
	}

	doorbell, err := canonicalV19HerdrWorkerWakeDoorbellFor(CanonicalV19HerdrWorkerWakeDoorbellInput{
		OperationID: request.OperationID, AttemptID: request.AttemptID,
		ExecutorBindingID: request.ExecutorBindingID, PendingThroughOrdinal: request.PendingThroughOrdinal,
	})
	if err != nil {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: %w", err)
	}
	if request.DoorbellDigest != doorbell.Digest {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: %w: persisted doorbell digest does not match exact Herdr doorbell bytes",
			ErrCanonicalV19WorkerWakeNotCurrent)
	}

	executorKey, err := parseCanonicalV19HerdrExecutorProviderKey(request.ProviderExecutorKey)
	if err != nil {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: %w: invalid provider Executor key: %v",
			ErrCanonicalV19WorkerWakeNotCurrent, err)
	}
	sessionKey, err := parseCanonicalV19HerdrSessionProviderKey(current.ProviderSessionKey)
	if err != nil {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: %w: invalid provider Session key: %v",
			ErrCanonicalV19WorkerWakeNotCurrent, err)
	}
	expectedSession := herdr.SessionName(current.FleetID)
	if executorKey.SessionName != expectedSession || sessionKey.SessionName != expectedSession ||
		executorKey.WorkspaceID != sessionKey.WorkspaceID || executorKey.TabID != sessionKey.TabID || executorKey.PaneID != sessionKey.PaneID {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: %w: provider Executor and Session identities differ",
			ErrCanonicalV19WorkerWakeNotCurrent)
	}

	client := deps.clientFor(expectedSession)
	if client == nil {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: provider client is unavailable")
	}
	observed := observeCanonicalV19HerdrWorkerWake(ctx, current, executorKey, sessionKey, client, deps.processAlive)
	if current.Current.State != "prepared" {
		return reconcileCanonicalV19SubmittedHerdrWorkerWake(ctx, homeDir, current, observed, deps.now)
	}
	if !current.PendingInput {
		observed.PendingInput = false
		observed.Reason = canonicalV19HerdrLaunchJoinReason(observed.Reason, "all WorkerInput through the persisted pending boundary is already acknowledged before submission")
		observed = finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
		return classifyCanonicalV19HerdrWorkerWakeNoEffect(ctx, homeDir, current, observed, deps.now)
	}

	switch observed.State {
	case canonicalV19HerdrWorkerWakeReady:
	case canonicalV19HerdrWorkerWakeBlocked:
		observed.Reason = canonicalV19HerdrLaunchJoinReason(observed.Reason, "Herdr v0.8.2 guarantees agent_blocked rejects before terminal input")
		observed = finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
		return classifyCanonicalV19HerdrWorkerWakeNoEffect(ctx, homeDir, current, observed, deps.now)
	case canonicalV19HerdrWorkerWakeCeased:
		return classifyCanonicalV19HerdrWorkerWakeNoEffect(ctx, homeDir, current, observed, deps.now)
	case canonicalV19HerdrWorkerWakeNotReady:
		return "prepared", fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: exact provider agent is not ready: %s", observed.Reason)
	case canonicalV19HerdrWorkerWakeMismatch:
		return "prepared", fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: provider ownership is unresolved: %s", observed.Reason)
	case canonicalV19HerdrWorkerWakeUnknown:
		return "prepared", fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: provider observation is unknown: %s", observed.Reason)
	default:
		return "prepared", fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: provider observation state %q is invalid", observed.State)
	}

	submittedAt := canonicalV19HerdrSessionTimestampAfter(deps.now(), current.Current.StateChangedAt)
	if _, err := SubmitCanonicalV19WorkerWake(ctx, homeDir, operationID, submittedAt, observed.EvidenceDigest); err != nil {
		return "prepared", err
	}

	current, err = readCanonicalV19HerdrWorkerWakeCurrent(ctx, homeDir, operationID)
	if err != nil {
		return "submitted", fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: revalidate after submit before provider mutation: %w", err)
	}
	if current.Current.State != "submitted" {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: %w: operation changed after submit to %q",
			ErrCanonicalV19WorkerWakeTransition, current.Current.State)
	}
	if !current.PendingInput {
		observed.PendingInput = false
		observed.Reason = "all WorkerInput through the persisted pending boundary became acknowledged after durable submit and before first provider mutation"
		observed = finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
		return classifyCanonicalV19HerdrWorkerWakeNoEffect(ctx, homeDir, current, observed, deps.now)
	}

	observed = observeCanonicalV19HerdrWorkerWake(ctx, current, executorKey, sessionKey, client, deps.processAlive)
	switch observed.State {
	case canonicalV19HerdrWorkerWakeReady:
	case canonicalV19HerdrWorkerWakeBlocked, canonicalV19HerdrWorkerWakeNotReady, canonicalV19HerdrWorkerWakeCeased:
		observed.Reason = canonicalV19HerdrLaunchJoinReason(observed.Reason, "no WorkerWake provider mutation was attempted after durable submit")
		observed = finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
		return classifyCanonicalV19HerdrWorkerWakeNoEffect(ctx, homeDir, current, observed, deps.now)
	case canonicalV19HerdrWorkerWakeMismatch, canonicalV19HerdrWorkerWakeUnknown:
		observed.Reason = canonicalV19HerdrLaunchJoinReason(observed.Reason, "provider identity became unresolved after durable submit and before first provider mutation")
		observed = finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
		return classifyCanonicalV19HerdrWorkerWakeUncertain(ctx, homeDir, current, observed, deps.now, nil)
	default:
		return "submitted", fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: provider observation state %q is invalid", observed.State)
	}

	performErr := client.AgentPromptContext(ctx, executorKey.PaneID, doorbell.Text)
	if performErr == nil {
		observed.ProviderAccepted = true
		observed.Reason = "Herdr v0.8.2 agent prompt completed the exact doorbell text and Enter writes to the exact recognized agent"
		observed = finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
		return completeCanonicalV19ObservedHerdrWorkerWake(ctx, homeDir, current, observed, deps.now)
	}
	if herdr.IsProcessNotStarted(performErr) || herdr.IsAgentPromptPreSideEffectRejection(performErr) {
		observed.Reason = canonicalV19HerdrLaunchJoinReason(observed.Reason,
			"Herdr agent prompt produced positive pre-side-effect rejection evidence: "+canonicalV19HerdrSessionErrorText(performErr))
		observed = finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
		return classifyCanonicalV19HerdrWorkerWakeNoEffect(ctx, homeDir, current, observed, deps.now)
	}

	observed = observeCanonicalV19HerdrWorkerWake(ctx, current, executorKey, sessionKey, client, deps.processAlive)
	observed.Reason = canonicalV19HerdrLaunchJoinReason(observed.Reason,
		"Herdr agent prompt outcome is ambiguous: "+canonicalV19HerdrSessionErrorText(performErr))
	observed = finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
	return classifyCanonicalV19HerdrWorkerWakeUncertain(ctx, homeDir, current, observed, deps.now, performErr)
}

func reconcileCanonicalV19SubmittedHerdrWorkerWake(
	ctx context.Context,
	homeDir string,
	current canonicalV19HerdrWorkerWakeCurrent,
	observed canonicalV19HerdrWorkerWakeObservation,
	now func() time.Time,
) (string, error) {
	observed.Reason = canonicalV19HerdrLaunchJoinReason(observed.Reason,
		"Herdr v0.8.2 agent prompt exposes no provider receipt or provider-enforced operation-key idempotency; submitted WorkerWake cannot be replayed")
	observed = finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
	if current.Current.State == "submitted" {
		return classifyCanonicalV19HerdrWorkerWakeUncertain(ctx, homeDir, current, observed, now, nil)
	}
	return "uncertain", fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: operation remains uncertain: %s", observed.Reason)
}

func completeCanonicalV19ObservedHerdrWorkerWake(
	ctx context.Context,
	homeDir string,
	current canonicalV19HerdrWorkerWakeCurrent,
	observed canonicalV19HerdrWorkerWakeObservation,
	now func() time.Time,
) (string, error) {
	observedAt := canonicalV19HerdrSessionTimestampAfter(now(), current.Current.StateChangedAt)
	if err := CompleteCanonicalV19WorkerWake(ctx, homeDir, CanonicalV19WorkerWakeSucceededEvidence{
		OperationID: current.Current.Request.OperationID, ObservedAt: observedAt, EvidenceDigest: observed.EvidenceDigest,
	}); err != nil {
		return current.Current.State, err
	}
	return "succeeded", nil
}

func classifyCanonicalV19HerdrWorkerWakeNoEffect(
	ctx context.Context,
	homeDir string,
	current canonicalV19HerdrWorkerWakeCurrent,
	observed canonicalV19HerdrWorkerWakeObservation,
	now func() time.Time,
) (string, error) {
	observedAt := canonicalV19HerdrSessionTimestampAfter(now(), current.Current.StateChangedAt)
	if err := ClassifyCanonicalV19WorkerWake(ctx, homeDir, CanonicalV19WorkerWakeTransitionInput{
		OperationID: current.Current.Request.OperationID, State: "no-effect",
		ObservedAt: observedAt, EvidenceDigest: observed.EvidenceDigest,
	}); err != nil {
		return current.Current.State, err
	}
	reason := observed.Reason
	if reason == "" {
		reason = "positive evidence proves no WorkerWake provider mutation occurred"
	}
	return "no-effect", fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: %s", reason)
}

func classifyCanonicalV19HerdrWorkerWakeUncertain(
	ctx context.Context,
	homeDir string,
	current canonicalV19HerdrWorkerWakeCurrent,
	observed canonicalV19HerdrWorkerWakeObservation,
	now func() time.Time,
	performErr error,
) (string, error) {
	if current.Current.State == "uncertain" {
		reason := observed.Reason
		if reason == "" {
			reason = "strongest provider evidence cannot classify exact WorkerWake acceptance"
		}
		return "uncertain", fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: operation remains uncertain: %s", reason)
	}
	observedAt := canonicalV19HerdrSessionTimestampAfter(now(), current.Current.StateChangedAt)
	if err := ClassifyCanonicalV19WorkerWake(ctx, homeDir, CanonicalV19WorkerWakeTransitionInput{
		OperationID: current.Current.Request.OperationID, State: "uncertain",
		ObservedAt: observedAt, EvidenceDigest: observed.EvidenceDigest,
	}); err != nil {
		return current.Current.State, err
	}
	reason := observed.Reason
	if performErr != nil {
		reason = canonicalV19HerdrLaunchJoinReason(reason, "provider error: "+canonicalV19HerdrSessionErrorText(performErr))
	}
	if reason == "" {
		reason = "strongest provider evidence cannot classify exact WorkerWake acceptance"
	}
	return "uncertain", fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: operation is uncertain: %s", reason)
}
