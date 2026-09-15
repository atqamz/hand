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

// ReconcileCanonicalV19HerdrWorkerWake reconciles one exact mechanism-only WorkerWake.
// The selected managed provider is refused before submission until its ExecutorBinding
// proves exact live execution identity. This path never creates WorkerInput acknowledgement evidence.
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
	unsupportedErr := canonicalV19HerdrCapabilityUnsupported(
		"WorkerWake", "exact live execution identity",
	)
	if current.Current.State == "uncertain" {
		return current.Current.State, unsupportedErr
	}
	state := "uncertain"
	if current.Current.State == "prepared" {
		state = "no-effect"
	}
	if err := ClassifyCanonicalV19WorkerWake(ctx, homeDir, CanonicalV19WorkerWakeTransitionInput{
		OperationID:    operationID,
		State:          state,
		ObservedAt:     canonicalV19HerdrSessionTimestampAfter(deps.now(), current.Current.StateChangedAt),
		EvidenceDigest: canonicalV19HerdrUnsupportedEvidenceDigest("WorkerWake", operationID, request.RequestDigest, state),
	}); err != nil {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: persist unsupported %s transition: %w", state, err)
	}
	return state, unsupportedErr
}
