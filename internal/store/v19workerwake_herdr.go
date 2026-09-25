package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atqamz/hand/internal/herdr"
)

type canonicalV19HerdrWorkerWakeCurrent struct {
	Current            canonicalV19WorkerWakeCurrent
	ProviderSessionKey string
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
	clientFor func(string) canonicalV19HerdrWorkerWakeClient
	now       func() time.Time
}

// ReconcileCanonicalV19HerdrWorkerWake reconciles one exact mechanism-only WorkerWake.
// The selected managed provider is refused before submission until its ExecutorBinding
// proves exact live execution identity. This path never creates WorkerInput acknowledgement evidence.
func ReconcileCanonicalV19HerdrWorkerWake(ctx context.Context, homeDir, operationID string) (string, error) {
	return reconcileCanonicalV19HerdrWorkerWake(ctx, homeDir, operationID, canonicalV19HerdrWorkerWakeDeps{
		clientFor: func(sessionName string) canonicalV19HerdrWorkerWakeClient {
			return herdr.NewManagedSessionClient(sessionName)
		},
		now: time.Now,
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
	if deps.clientFor == nil || deps.now == nil {
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
	if request.AdapterRef != CanonicalV19HerdrSessionAdapterRef {
		return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: %w: adapter %q is not %q",
			ErrCanonicalV19WorkerWakeNotCurrent, request.AdapterRef, CanonicalV19HerdrSessionAdapterRef)
	}
	unsupportedErr := canonicalV19HerdrCapabilityUnsupported(
		"WorkerWake", "exact live execution identity",
	)
	if current.Current.State == "uncertain" {
		return current.Current.State, unsupportedErr
	}
	if current.Current.State == "prepared" {
		if err := classifyCanonicalV19WorkerWakePreparedNoEffect(ctx, homeDir, CanonicalV19WorkerWakeTransitionInput{
			OperationID:    operationID,
			State:          "no-effect",
			ObservedAt:     canonicalV19HerdrSessionTimestampAfter(deps.now(), current.Current.StateChangedAt),
			EvidenceDigest: canonicalV19HerdrUnsupportedEvidenceDigest("WorkerWake", operationID, request.RequestDigest, "no-effect"),
		}); err == nil {
			return "no-effect", unsupportedErr
		} else if errors.Is(err, ErrCanonicalV19WorkerWakeTransition) {
			return reconcileCanonicalV19HerdrWorkerWake(ctx, homeDir, operationID, deps)
		} else {
			return current.Current.State, fmt.Errorf("reconcile canonical v19 Herdr WorkerWake: persist unsupported no-effect transition: %w", err)
		}
	}
	state := "uncertain"
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

func classifyCanonicalV19WorkerWakePreparedNoEffect(
	ctx context.Context,
	homeDir string,
	input CanonicalV19WorkerWakeTransitionInput,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCanonicalV19WorkerWakeTransitionInput(input); err != nil {
		return err
	}
	if input.State != "no-effect" {
		return fmt.Errorf("classify canonical v19 WorkerWake: prepared settlement requires no-effect state")
	}
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return canonicalV19WorkerWakeWriteError("classify", "begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return fmt.Errorf("classify canonical v19 WorkerWake: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE external_operation
		SET state='no-effect',state_changed_at=?,state_evidence_digest=?,finalized_at=?
		WHERE id=? AND kind='worker-wake' AND state='prepared'`, input.ObservedAt, input.EvidenceDigest,
		input.ObservedAt, input.OperationID)
	if err != nil {
		return canonicalV19WorkerWakeConstraintError("classify", "settle prepared exact operation", input.OperationID, err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return canonicalV19WorkerWakeWriteError("classify", "count prepared state transition", err)
	}
	if changed != 1 {
		return fmt.Errorf("classify canonical v19 WorkerWake: %w: operation %q was not prepared", ErrCanonicalV19WorkerWakeTransition, input.OperationID)
	}
	if err := tx.Commit(); err != nil {
		return canonicalV19WorkerWakeWriteError("classify", "commit writer", err)
	}
	committed = true
	return nil
}
