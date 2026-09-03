package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/store"
)

var errLegacyV18CutoverHerdrUnsafe = errors.New("legacy v18 cutover Herdr state is not quiescent")

type legacyV18CutoverHerdrBlocker struct {
	Code    string
	Subject string
	Detail  string
}

type legacyV18CutoverHerdrBlockedError struct {
	Blockers []legacyV18CutoverHerdrBlocker
}

func (e *legacyV18CutoverHerdrBlockedError) Error() string {
	if e == nil || len(e.Blockers) == 0 {
		return errLegacyV18CutoverHerdrUnsafe.Error()
	}
	parts := make([]string, 0, len(e.Blockers))
	for _, blocker := range e.Blockers {
		part := blocker.Code + " " + blocker.Subject
		if blocker.Detail != "" {
			part += ": " + blocker.Detail
		}
		parts = append(parts, part)
	}
	return errLegacyV18CutoverHerdrUnsafe.Error() + ": " + strings.Join(parts, "; ")
}

func (e *legacyV18CutoverHerdrBlockedError) Unwrap() error {
	return errLegacyV18CutoverHerdrUnsafe
}

type legacyV18CutoverHerdrEvidence struct {
	Sessions []herdr.SessionObservation
}

type legacyV18CutoverHerdrInventory interface {
	WorkspaceList() ([]herdr.Workspace, error)
	TabList(string) ([]herdr.Tab, error)
	PaneGet(string) (herdr.Pane, error)
}

type legacyV18CutoverHerdrDeps struct {
	observeSession func(context.Context, string) herdr.SessionObservation
	inventoryFor   func(string) legacyV18CutoverHerdrInventory
}

func defaultLegacyV18CutoverHerdrDeps() legacyV18CutoverHerdrDeps {
	return legacyV18CutoverHerdrDeps{
		observeSession: func(ctx context.Context, session string) herdr.SessionObservation {
			return herdr.NewManagedSessionClient(session).ObserveSession(ctx)
		},
		inventoryFor: func(session string) legacyV18CutoverHerdrInventory {
			return newHerdrSessionClient(session)
		},
	}
}

func observeLegacyV18CutoverHerdr(ctx context.Context, guard *store.LegacyV18CutoverGuard) (legacyV18CutoverHerdrEvidence, error) {
	if guard == nil {
		return legacyV18CutoverHerdrEvidence{}, store.ErrLegacyV18CutoverGuardClosed
	}
	plan, err := guard.ObservationPlan()
	if err != nil {
		return legacyV18CutoverHerdrEvidence{}, fmt.Errorf("read held legacy v18 cutover observation plan: %w", err)
	}
	return observeLegacyV18CutoverHerdrPlan(ctx, plan, defaultLegacyV18CutoverHerdrDeps())
}

func observeLegacyV18CutoverHerdrPlan(ctx context.Context, plan store.LegacyV18CutoverObservationPlan, deps legacyV18CutoverHerdrDeps) (legacyV18CutoverHerdrEvidence, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if deps.observeSession == nil || deps.inventoryFor == nil {
		return legacyV18CutoverHerdrEvidence{}, errors.New("legacy v18 cutover Herdr dependencies are incomplete")
	}

	currentSession := herdr.SessionName(plan.FleetID)
	recordedBySession := map[string][]store.LegacyV18CutoverHerdrObservation{
		currentSession: nil,
		"default":      nil,
	}
	var blockers []legacyV18CutoverHerdrBlocker
	addBlocker := func(code, subject, detail string) {
		blockers = append(blockers, legacyV18CutoverHerdrBlocker{Code: code, Subject: subject, Detail: detail})
	}
	for _, recorded := range plan.Herdr {
		session, ok := legacyV18CutoverHerdrSession(recorded.Session, currentSession)
		if !ok {
			addBlocker("herdr-recorded-session-unrecognized", fmt.Sprintf("attempt:%d", recorded.AttemptID), fmt.Sprintf("session=%q", recorded.Session))
			continue
		}
		recordedBySession[session] = append(recordedBySession[session], recorded)
	}

	evidence := legacyV18CutoverHerdrEvidence{Sessions: make([]herdr.SessionObservation, 0, 2)}
	for _, session := range []string{currentSession, "default"} {
		if err := ctx.Err(); err != nil {
			return legacyV18CutoverHerdrEvidence{}, err
		}
		observation := deps.observeSession(ctx, session)
		evidence.Sessions = append(evidence.Sessions, observation)
		if observation.Name != session {
			addBlocker("herdr-session-identity-mismatch", "session:"+session, fmt.Sprintf("provider named %q", observation.Name))
			continue
		}
		switch observation.State {
		case herdr.SessionStopped:
			continue
		case herdr.SessionRunningCompatible:
			observeLegacyV18CutoverHerdrRunningSession(session, currentSession, recordedBySession[session], deps.inventoryFor(session), addBlocker)
		case herdr.SessionUnknown, herdr.SessionIncompatible:
			addBlocker("herdr-session-unobservable", "session:"+session, fmt.Sprintf("state=%q reason=%q", observation.State, observation.Reason))
		default:
			addBlocker("herdr-session-unobservable", "session:"+session, fmt.Sprintf("state=%q", observation.State))
		}
	}

	if len(blockers) != 0 {
		sort.Slice(blockers, func(i, j int) bool {
			if blockers[i].Code != blockers[j].Code {
				return blockers[i].Code < blockers[j].Code
			}
			if blockers[i].Subject != blockers[j].Subject {
				return blockers[i].Subject < blockers[j].Subject
			}
			return blockers[i].Detail < blockers[j].Detail
		})
		return legacyV18CutoverHerdrEvidence{}, &legacyV18CutoverHerdrBlockedError{Blockers: blockers}
	}
	return evidence, nil
}

func legacyV18CutoverHerdrSession(recorded, current string) (string, bool) {
	if recorded == "" || recorded == "default" {
		return "default", true
	}
	if recorded == current {
		return current, true
	}
	return "", false
}

func observeLegacyV18CutoverHerdrRunningSession(session, currentSession string, recorded []store.LegacyV18CutoverHerdrObservation, inventory legacyV18CutoverHerdrInventory, addBlocker func(string, string, string)) {
	if inventory == nil {
		addBlocker("herdr-provider-unavailable", "session:"+session, "Herdr inventory client is unavailable")
		return
	}
	workspaces, err := inventory.WorkspaceList()
	if err != nil {
		addBlocker("herdr-workspace-inventory-unobservable", "session:"+session, err.Error())
		return
	}

	recordedWorkspaces := make(map[string][]store.LegacyV18CutoverHerdrObservation)
	recordedTabs := make(map[string][]store.LegacyV18CutoverHerdrObservation)
	for _, expected := range recorded {
		recordedWorkspaces[expected.WorkspaceID] = append(recordedWorkspaces[expected.WorkspaceID], expected)
		recordedTabs[expected.TabID] = append(recordedTabs[expected.TabID], expected)
	}

	workspaceIDs := make(map[string]struct{}, len(workspaces))
	tabIDs := make(map[string]struct{})
	for _, workspace := range workspaces {
		workspaceSubject := "workspace:" + workspace.WorkspaceID
		if workspace.WorkspaceID == "" {
			addBlocker("herdr-workspace-identity-invalid", "session:"+session, fmt.Sprintf("label=%q has empty workspace_id", workspace.Label))
			continue
		}
		if _, exists := workspaceIDs[workspace.WorkspaceID]; exists {
			addBlocker("herdr-workspace-id-collision", workspaceSubject, "provider inventory returned the same workspace identity more than once")
		} else {
			workspaceIDs[workspace.WorkspaceID] = struct{}{}
		}
		if strings.HasPrefix(workspace.Label, "hand:") {
			code := "herdr-current-hand-resource-live"
			if session != currentSession {
				code = "herdr-legacy-hand-resource-live"
			}
			addBlocker(code, workspaceSubject, fmt.Sprintf("label=%q", workspace.Label))
		}
		recordedWorkspace := recordedWorkspaces[workspace.WorkspaceID]
		for _, expected := range recordedWorkspace {
			addBlocker("herdr-recorded-workspace-live", fmt.Sprintf("attempt:%d", expected.AttemptID), fmt.Sprintf("session=%q workspace_id=%q label=%q", session, workspace.WorkspaceID, workspace.Label))
		}
		if !strings.HasPrefix(workspace.Label, "hand:") && len(recordedWorkspace) == 0 {
			continue
		}

		tabs, err := inventory.TabList(workspace.WorkspaceID)
		if err != nil {
			addBlocker("herdr-tab-inventory-unobservable", workspaceSubject, err.Error())
			continue
		}
		for _, tab := range tabs {
			tabSubject := "tab:" + tab.TabID
			if tab.TabID == "" {
				addBlocker("herdr-tab-identity-invalid", workspaceSubject, fmt.Sprintf("label=%q has empty tab_id", tab.Label))
				continue
			}
			if tab.WorkspaceID != workspace.WorkspaceID {
				addBlocker("herdr-tab-workspace-mismatch", tabSubject, fmt.Sprintf("workspace_id=%q inventory_workspace_id=%q", tab.WorkspaceID, workspace.WorkspaceID))
			}
			if _, exists := tabIDs[tab.TabID]; exists {
				addBlocker("herdr-tab-id-collision", tabSubject, "provider inventory returned the same tab identity more than once")
			} else {
				tabIDs[tab.TabID] = struct{}{}
			}
			for _, expected := range recordedTabs[tab.TabID] {
				addBlocker("herdr-recorded-tab-live", fmt.Sprintf("attempt:%d", expected.AttemptID), fmt.Sprintf("session=%q tab_id=%q workspace_id=%q label=%q", session, tab.TabID, workspace.WorkspaceID, tab.Label))
			}
		}
	}

	for _, expected := range recorded {
		pane, err := inventory.PaneGet(expected.PaneID)
		if err != nil {
			if isHerdrNotFound(err) {
				continue
			}
			addBlocker("herdr-recorded-pane-unobservable", fmt.Sprintf("attempt:%d", expected.AttemptID), err.Error())
			continue
		}
		addBlocker("herdr-recorded-pane-live", fmt.Sprintf("attempt:%d", expected.AttemptID), fmt.Sprintf("session=%q pane_id=%q tab_id=%q workspace_id=%q", session, pane.PaneID, pane.TabID, pane.WorkspaceID))
	}
}
