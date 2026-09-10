package store

import (
	"context"
	"errors"
	"fmt"

	handgit "github.com/atqamz/hand/internal/git"
	"github.com/atqamz/hand/internal/herdr"
)

func observeCanonicalV19HerdrWorkerWake(
	ctx context.Context,
	current canonicalV19HerdrWorkerWakeCurrent,
	executorKey canonicalV19HerdrExecutorProviderKey,
	sessionKey canonicalV19HerdrSessionProviderKey,
	client canonicalV19HerdrWorkerWakeClient,
	processAlive func(int) (bool, error),
) canonicalV19HerdrWorkerWakeObservation {
	request := current.Current.Request
	observed := canonicalV19HerdrWorkerWakeObservation{
		PendingInput: current.PendingInput,
		WorkspaceID:  executorKey.WorkspaceID,
		TabID:        executorKey.TabID,
		PaneID:       executorKey.PaneID,
		ProcessID:    executorKey.ProcessID,
	}

	alive, aliveErr := processAlive(executorKey.ProcessID)
	observed.ProcessAlive = alive
	if aliveErr == nil && !alive {
		observed.State = canonicalV19HerdrWorkerWakeCeased
		observed.Reason = "exact provider Executor PID is absent"
		return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
	}

	session := client.ObserveSession(ctx)
	if session.Name != executorKey.SessionName || session.State != herdr.SessionRunningCompatible {
		observed.State = canonicalV19HerdrWorkerWakeUnknown
		observed.Reason = fmt.Sprintf("exact Herdr session %q is %q", executorKey.SessionName, session.State)
		if session.Reason != "" {
			observed.Reason += ": " + session.Reason
		}
		if aliveErr != nil {
			observed.Reason = canonicalV19HerdrLaunchJoinReason(observed.Reason, "process liveness probe failed: "+aliveErr.Error())
		}
		return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
	}

	workspaces, err := client.WorkspaceListContext(ctx)
	if err != nil {
		observed.State = canonicalV19HerdrWorkerWakeUnknown
		observed.Reason = "list exact Herdr session workspaces: " + canonicalV19HerdrSessionErrorText(err)
		return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
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
		observed.State = canonicalV19HerdrWorkerWakeMismatch
		if workspaceMatches == 0 {
			observed.Reason = "exact Session workspace is absent while the provider Executor PID remains live"
		} else {
			observed.Reason = "Herdr workspace inventory returned the exact Session workspace more than once"
		}
		return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
	}
	if workspace.Label != canonicalV19HerdrSessionWorkspaceLabel(request.SessionBindingID) {
		observed.State = canonicalV19HerdrWorkerWakeMismatch
		observed.Reason = "exact workspace identity no longer carries the SessionBinding locator"
		return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
	}

	tabs, err := client.TabList(sessionKey.WorkspaceID)
	if err != nil {
		observed.State = canonicalV19HerdrWorkerWakeUnknown
		observed.Reason = "list exact Session workspace tabs: " + canonicalV19HerdrSessionErrorText(err)
		return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
	}
	if len(tabs) != 1 || tabs[0].TabID != sessionKey.TabID || tabs[0].WorkspaceID != sessionKey.WorkspaceID {
		observed.State = canonicalV19HerdrWorkerWakeMismatch
		observed.Reason = "Session workspace root tab no longer matches the persisted provider Session key"
		return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
	}

	pane, err := client.PaneGetContext(ctx, sessionKey.PaneID)
	if err != nil {
		if errors.Is(err, herdr.ErrNotFound) {
			observed.State = canonicalV19HerdrWorkerWakeMismatch
			observed.Reason = "exact Session root pane is absent while the provider Executor PID remains live"
		} else {
			observed.State = canonicalV19HerdrWorkerWakeUnknown
			observed.Reason = "observe exact Session root pane: " + canonicalV19HerdrSessionErrorText(err)
		}
		return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
	}
	observed.PaneCwd = pane.Cwd
	observed.Agent = pane.Agent
	observed.AgentStatus = pane.AgentStatus
	if pane.PaneID != sessionKey.PaneID || pane.TabID != sessionKey.TabID || pane.WorkspaceID != sessionKey.WorkspaceID {
		observed.State = canonicalV19HerdrWorkerWakeMismatch
		observed.Reason = "exact root pane parent identities differ from the provider Session key"
		return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
	}
	if pane.Cwd == "" || !handgit.SamePath(pane.Cwd, current.WorktreePath) {
		observed.State = canonicalV19HerdrWorkerWakeMismatch
		observed.Reason = "exact root pane cwd differs from the immutable WorktreeBinding"
		return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
	}

	info, err := client.PaneProcessInfo(sessionKey.PaneID)
	if err != nil {
		observed.State = canonicalV19HerdrWorkerWakeUnknown
		observed.Reason = "observe exact pane process identity: " + canonicalV19HerdrSessionErrorText(err)
		return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
	}
	observed.ShellPID = info.ShellPID
	observed.ProcessGroupID = info.ForegroundProcessGroupID
	if info.PaneID != sessionKey.PaneID || info.ShellPID <= 0 || info.ForegroundProcessGroupID <= 0 {
		observed.State = canonicalV19HerdrWorkerWakeUnknown
		observed.Reason = "pane process evidence lacks exact pane/shell/process-group identity"
		return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
	}

	matches := 0
	for _, process := range info.ForegroundProcesses {
		if process.PID != executorKey.ProcessID {
			continue
		}
		observed.ProcessDigest = canonicalV19HerdrProcessDigest(process)
		if observed.ProcessDigest != executorKey.ProcessDigest {
			observed.State = canonicalV19HerdrWorkerWakeMismatch
			observed.Reason = "provider Executor PID now carries different argv/cwd identity"
			return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
		}
		matches++
	}
	if matches > 1 {
		observed.State = canonicalV19HerdrWorkerWakeMismatch
		observed.Reason = "foreground process evidence returned the exact provider Executor PID more than once"
		return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
	}
	if matches == 0 {
		if aliveErr != nil {
			observed.State = canonicalV19HerdrWorkerWakeUnknown
			observed.Reason = "exact provider Executor is absent from foreground evidence and process liveness probe failed: " + aliveErr.Error()
			return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
		}
		if alive {
			observed.State = canonicalV19HerdrWorkerWakeUnknown
			observed.Reason = "exact provider Executor PID remains live but is absent from exact pane foreground process evidence"
			return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
		}
		observed.State = canonicalV19HerdrWorkerWakeCeased
		observed.Reason = "exact provider Executor PID is absent"
		return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
	}
	if info.ForegroundProcessGroupID != executorKey.ProcessGroup {
		observed.State = canonicalV19HerdrWorkerWakeMismatch
		observed.Reason = "exact provider Executor PID is no longer in its persisted foreground process group"
		return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
	}
	if pane.Agent == "" {
		observed.State = canonicalV19HerdrWorkerWakeNotReady
		observed.Reason = "exact provider Executor remains live but Herdr does not currently recognize its agent"
		return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
	}
	if pane.AgentStatus == herdr.StatusBlocked {
		observed.State = canonicalV19HerdrWorkerWakeBlocked
		observed.Reason = "exact recognized Herdr agent is blocked on interactive input"
		return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
	}
	observed.State = canonicalV19HerdrWorkerWakeReady
	return finalizeCanonicalV19HerdrWorkerWakeObservation(current, observed)
}
