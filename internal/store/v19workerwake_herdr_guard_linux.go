//go:build linux

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/atqamz/hand/internal/execguard"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/osfacts"
)

const canonicalV19HerdrWorkerWakeCallTimeout = 30 * time.Second

// Tx A and Tx B, then W(B), the constant doorbell through `herdr agent prompt`, and W(B) again.
func wakeCanonicalV19HerdrGuarded(
	ctx context.Context,
	homeDir string,
	input CanonicalV19WorkerWakePrepareInput,
	deps canonicalV19HerdrWorkerWakeDeps,
) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	input.DoorbellDigest = CanonicalV19HerdrWorkerWakeDoorbellDigest()
	if _, err := PrepareCanonicalV19WorkerWake(ctx, homeDir, input); err != nil {
		return "", err
	}
	abandon := func(cause error) (string, error) {
		state, err := reconcileCanonicalV19HerdrWorkerWake(ctx, homeDir, input.OperationID, deps)
		return state, errors.Join(fmt.Errorf("wake canonical v19 Herdr Worker: %w", cause), err)
	}
	current, err := readCanonicalV19HerdrWorkerWakeCurrent(ctx, homeDir, input.OperationID)
	if err != nil {
		return abandon(err)
	}
	key, err := parseCanonicalV19ExecGuardKey(current.Current.Request.ProviderExecutorKey)
	if err != nil {
		return abandon(fmt.Errorf("the ExecutorBinding key has no exec-guard grammar: %w", err))
	}
	dir, err := canonicalV19ExecGuardDir(homeDir, current.LaunchOperationID)
	if err != nil {
		return abandon(err)
	}
	client := deps.clientFor(key.Session.SessionName)
	if client == nil {
		return abandon(errors.New("provider client is unavailable"))
	}
	submittedAt := canonicalV19HerdrSessionTimestampAfter(deps.now(), current.Current.StateChangedAt)
	if _, err := SubmitCanonicalV19WorkerWake(ctx, homeDir, input.OperationID, submittedAt,
		canonicalV19HerdrGuardWakeEvidenceDigest(current, "submitted", "")); err != nil {
		return abandon(err)
	}
	current.Current.StateChangedAt = submittedAt
	settle := func(state, reason string) (string, error) {
		observedAt := canonicalV19HerdrSessionTimestampAfter(deps.now(), current.Current.StateChangedAt)
		evidence := canonicalV19HerdrGuardWakeEvidenceDigest(current, state, reason)
		var err error
		if state == "succeeded" {
			err = CompleteCanonicalV19WorkerWake(ctx, homeDir, CanonicalV19WorkerWakeSucceededEvidence{
				OperationID: input.OperationID, ObservedAt: observedAt, EvidenceDigest: evidence,
			})
		} else {
			err = ClassifyCanonicalV19WorkerWake(ctx, homeDir, CanonicalV19WorkerWakeTransitionInput{
				OperationID: input.OperationID, State: state, ObservedAt: observedAt, EvidenceDigest: evidence,
			})
		}
		switch {
		case err != nil:
			return canonicalV19HerdrWorkerWakeStoredState(ctx, homeDir, input.OperationID),
				fmt.Errorf("wake canonical v19 Herdr Worker: persist %s: %w", state, err)
		case state == "succeeded":
			return state, nil
		}
		return state, fmt.Errorf("wake canonical v19 Herdr Worker: %s: %s", state, reason)
	}
	if reason := checkCanonicalV19HerdrGuardWake(ctx, current, key, dir, client); reason != "" {
		return settle("no-effect", "W(B) failed before delivery: "+reason)
	}
	promptCtx, cancel := context.WithTimeout(ctx, canonicalV19HerdrWorkerWakeCallTimeout)
	promptErr := client.AgentPromptContext(promptCtx, key.Session.PaneID, canonicalV19HerdrWorkerWakeDoorbell)
	cancel()
	switch {
	case herdr.IsAgentPromptPreSideEffectRejection(promptErr):
		return settle("rejected", "Herdr refused the doorbell before queueing input: "+canonicalV19HerdrSessionErrorText(promptErr))
	case herdr.IsProcessNotStarted(promptErr):
		return settle("no-effect", "the herdr client never started: "+canonicalV19HerdrSessionErrorText(promptErr))
	case promptErr != nil:
		return settle("uncertain", "the Herdr reply was lost; settling it needs the unqualified Herdr property O1: "+canonicalV19HerdrSessionErrorText(promptErr))
	}
	if reason := checkCanonicalV19HerdrGuardWake(ctx, current, key, dir, client); reason != "" {
		return settle("uncertain", "W(B) failed after delivery: "+reason)
	}
	return settle("succeeded", "")
}

// W(B) on Linux reads the recorded key and the OS facts first, so a binding whose Launch did
// not observe A(B), or a gone or ceased guard, never reaches Herdr.
func checkCanonicalV19HerdrGuardWake(
	parent context.Context,
	current canonicalV19HerdrWorkerWakeCurrent,
	key canonicalV19ExecGuardKey,
	dir string,
	client canonicalV19HerdrWorkerWakeClient,
) string {
	if key.Assoc != "observed" {
		return "the Launch recorded the pane association A(B) as " + key.Assoc
	}
	if key.Guard == nil || key.Root == nil {
		return "the ExecutorBinding key names no guard incarnation and harness root"
	}
	if live := osfacts.Observe(*key.Guard); live != osfacts.Alive {
		return "the exec guard is " + string(live)
	}
	if live := osfacts.Observe(*key.Root); live != osfacts.Alive {
		return "the harness root is " + string(live)
	}
	if _, err := execguard.ReadRecord(dir, execguard.KindCeased); err == nil {
		return "the exec guard recorded cessation"
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "the ceased record could not be read: " + err.Error()
	}
	if reason := checkCanonicalV19ExecGuardOSFacts(*key.Guard, key.Terminal); reason != "" {
		return reason
	}
	session, err := parseCanonicalV19HerdrSessionProviderKey(current.ProviderSessionKey)
	if err != nil || session != key.Session {
		return "the ExecutorBinding key names another pane than the SessionBinding"
	}
	ctx, cancel := context.WithTimeout(parent, canonicalV19HerdrWorkerWakeCallTimeout)
	defer cancel()
	if observed := client.ObserveSession(ctx); observed.Name != session.SessionName || observed.State != herdr.SessionRunningCompatible {
		return fmt.Sprintf("Herdr session %q is %q", session.SessionName, observed.State)
	}
	workspaces, err := client.WorkspaceListContext(ctx)
	if err != nil {
		return "list Herdr workspaces: " + canonicalV19HerdrSessionErrorText(err)
	}
	var matches []herdr.Workspace
	for _, workspace := range workspaces {
		if workspace.WorkspaceID == session.WorkspaceID {
			matches = append(matches, workspace)
		}
	}
	if len(matches) != 1 || matches[0].Label != canonicalV19HerdrSessionWorkspaceLabel(current.Current.Request.SessionBindingID) {
		return "Herdr reports no single Session workspace carrying the SessionBinding locator"
	}
	tabs, err := client.TabListContext(ctx, session.WorkspaceID)
	if err != nil || len(tabs) != 1 || tabs[0].TabID != session.TabID || tabs[0].WorkspaceID != session.WorkspaceID {
		return "the Session workspace's root tab differs from the SessionBinding"
	}
	pane, err := client.PaneGetContext(ctx, session.PaneID)
	if err != nil || pane.PaneID != session.PaneID || pane.TabID != session.TabID || pane.WorkspaceID != session.WorkspaceID {
		return "the Session root pane differs from the SessionBinding"
	}
	if assoc := canonicalV19ExecGuardAssociation(ctx, client, session.PaneID, *key.Guard, key.ProcessGroup); assoc != "observed" {
		return "the pane association A(B) is " + assoc
	}
	if pane.AgentStatus == herdr.StatusBlocked {
		return "Herdr reports the pane agent blocked"
	}
	return ""
}

func canonicalV19HerdrWorkerWakeStoredState(ctx context.Context, homeDir, operationID string) string {
	if current, err := readCanonicalV19HerdrWorkerWakeCurrent(ctx, homeDir, operationID); err == nil {
		return current.Current.State
	}
	state, _, _ := readCanonicalV19HerdrWorkerWakeTerminal(ctx, homeDir, operationID)
	return state
}

func canonicalV19HerdrGuardWakeEvidenceDigest(current canonicalV19HerdrWorkerWakeCurrent, state, reason string) string {
	request := current.Current.Request
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:herdr-exec-guard-worker-wake-observation:v1")
	writeCanonicalV19DigestField(hash, "operation_id", request.OperationID)
	writeCanonicalV19DigestField(hash, "request_digest", request.RequestDigest)
	writeCanonicalV19DigestField(hash, "provider_session_key", current.ProviderSessionKey)
	writeCanonicalV19DigestField(hash, "provider_executor_key", request.ProviderExecutorKey)
	writeCanonicalV19DigestField(hash, "doorbell_digest", request.DoorbellDigest)
	writeCanonicalV19DigestField(hash, "state", state)
	writeCanonicalV19DigestField(hash, "reason", reason)
	return hex.EncodeToString(hash.Sum(nil))
}
