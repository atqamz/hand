package runtime

import (
	"errors"
	"fmt"
	"strings"

	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/launch"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/worktree"
)

type herdrClient interface {
	FindWorkspaceByLabel(string) (herdr.Workspace, bool, error)
	WorkspaceList() ([]herdr.Workspace, error)
	WorkspaceCreate(string, map[string]string, string) (herdr.Workspace, herdr.Tab, herdr.Pane, error)
	WorkspaceClose(string) error
	TabList(string) ([]herdr.Tab, error)
	TabCreate(string, string, map[string]string, string) (herdr.Tab, herdr.Pane, error)
	TabRename(string, string) error
	TabClose(string) error
	PaneGet(string) (herdr.Pane, error)
	PaneProcessInfo(string) (herdr.ProcessInfo, error)
	PaneRunSpec(string, launch.LaunchSpec) error
	PaneSendKeys(string, ...string) error
	PaneRead(string, int) (string, error)
}

func newHerdrClient() herdrClient { return herdr.NewManagedClient() }

func newHerdrSessionClient(session string) herdrClient {
	if session == "" || session == "default" {
		return herdr.NewManagedClient()
	}
	return herdr.NewManagedSessionClient(session)
}

func (r *Runtime) herdrClient(session string) herdrClient {
	if r.deps.herdrFor != nil {
		return r.deps.herdrFor(session)
	}
	if r.deps.herdr != nil {
		return r.deps.herdr()
	}
	return newHerdrSessionClient(session)
}

func herdrWorkerEnvironment(client herdrClient) map[string]string {
	provider, ok := client.(interface{ WorkerEnvironment() map[string]string })
	if !ok {
		return nil
	}
	return provider.WorkerEnvironment()
}

func herdrWorkspaceLabelForSession(session, projectName string) string {
	if session == "" || session == "default" {
		return herdr.LegacyWorkspaceLabel(projectName)
	}
	return herdr.WorkspaceLabel(strings.TrimPrefix(session, "hand-"), projectName)
}

func acquireTaskWorkspace(client herdrClient, worktreePath string, env map[string]string, taskID, projectName, session string) (herdr.Workspace, herdr.Tab, herdr.Pane, func() error, error) {
	label := herdrWorkspaceLabelForSession(session, projectName)
	workspace, found, err := client.FindWorkspaceByLabel(label)
	createdWorkspace := false
	var rootTab herdr.Tab
	var rootPane herdr.Pane
	if err == nil && !found {
		workspace, rootTab, rootPane, err = client.WorkspaceCreate(worktreePath, env, label)
		createdWorkspace = err == nil
	}
	if err != nil {
		return herdr.Workspace{}, herdr.Tab{}, herdr.Pane{}, nil, fmt.Errorf("herdr workspace lookup/create failed: %w", err)
	}

	tab, pane, err := acquireTaskTab(client, createdWorkspace, workspace.WorkspaceID, worktreePath, env, taskID, rootTab, rootPane)
	if err != nil {
		return herdr.Workspace{}, herdr.Tab{}, herdr.Pane{}, nil, reportCleanup(err, rollbackHerdr(client, createdWorkspace, workspace.WorkspaceID, ""))
	}
	rollback := func() error {
		return rollbackHerdr(client, createdWorkspace, workspace.WorkspaceID, tab.TabID)
	}
	return workspace, tab, pane, rollback, nil
}

func acquireTaskTab(client herdrClient, createdWorkspace bool, workspaceID, worktreePath string, env map[string]string, taskID string, rootTab herdr.Tab, rootPane herdr.Pane) (herdr.Tab, herdr.Pane, error) {
	if createdWorkspace {
		if err := client.TabRename(rootTab.TabID, taskID); err != nil {
			return herdr.Tab{}, herdr.Pane{}, fmt.Errorf("herdr tab rename failed: %w", err)
		}
		return rootTab, rootPane, nil
	}
	tab, pane, err := client.TabCreate(workspaceID, worktreePath, env, taskID)
	if err != nil {
		return herdr.Tab{}, herdr.Pane{}, fmt.Errorf("herdr tab create failed: %w", err)
	}
	return tab, pane, nil
}

func rollbackHerdr(client herdrClient, createdWorkspace bool, workspaceID, tabID string) error {
	if createdWorkspace {
		return client.WorkspaceClose(workspaceID)
	}
	if tabID == "" {
		return nil
	}
	return closeTaskTab(client, workspaceID, tabID)
}

func closeTaskTab(client herdrClient, workspaceID, tabID string) error {
	if workspaceID == "" || tabID == "" {
		return nil
	}
	tabs, err := client.TabList(workspaceID)
	if err != nil {
		return err
	}
	found := false
	for _, tab := range tabs {
		if tab.TabID == tabID {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	if len(tabs) == 1 {
		return client.WorkspaceClose(workspaceID)
	}
	return client.TabClose(tabID)
}

func (r *Runtime) observeWorktreeLease(worktreePath, leaseID string) worktree.LeaseObservation {
	observe := r.deps.worktree.observeLease
	if observe == nil {
		observe = worktree.ObserveLease
	}
	return observe(worktreePath, leaseID)
}

func worktreeCleanupSettled(teardownWorktreeState string) bool {
	return teardownWorktreeState == state.TeardownResourceReleased || teardownWorktreeState == state.TeardownResourceAbandoned
}

// Settled covers abandonment as well as release: an attested relinquishment closes the question of
// what Hand still claims, even though the resource itself was never touched.
func herdrCleanupSettled(teardownHerdrState string) bool {
	return teardownHerdrState == state.TeardownResourceReleased || teardownHerdrState == state.TeardownResourceAbandoned
}

func incompleteHerdrOwnership(ownership state.Herdr) error {
	if ownership.WorkspaceID == "" && ownership.TabID == "" && ownership.PaneID == "" {
		return nil
	}
	if ownership.WorkspaceID == "" || ownership.TabID == "" || ownership.PaneID == "" {
		return fmt.Errorf("workspace, tab, and pane identity are incomplete (workspace=%q, tab=%q, pane=%q)", ownership.WorkspaceID, ownership.TabID, ownership.PaneID)
	}
	return nil
}

func hasHerdrIdentity(ownership state.Herdr) bool {
	return ownership.WorkspaceID != "" || ownership.TabID != "" || ownership.PaneID != ""
}

func observeHerdrOwnership(client herdrClient, expected state.Herdr, taskID, projectName string) (herdrObservation, error) {
	if err := incompleteHerdrOwnership(expected); err != nil {
		return herdrObservation{State: herdrOwnershipIncomplete}, nil
	}
	if expected.WorkspaceID == "" && expected.TabID == "" && expected.PaneID == "" {
		return herdrObservation{State: herdrOwnershipUnobserved}, nil
	}
	label := herdrWorkspaceLabelForSession(expected.Session, projectName)
	var workspace herdr.Workspace
	var found bool
	var err error
	if expected.Session == "" || expected.Session == "default" {
		workspace, found, err = client.FindWorkspaceByLabel(label)
		if err == nil && !found {
			var workspaces []herdr.Workspace
			workspaces, err = client.WorkspaceList()
			for _, candidate := range workspaces {
				if candidate.WorkspaceID == expected.WorkspaceID {
					workspace, found = candidate, true
					break
				}
			}
		}
	} else {
		workspace, found, err = client.FindWorkspaceByLabel(label)
	}
	if err != nil {
		return herdrObservation{}, fmt.Errorf("find Herdr workspace: %w", err)
	}
	if !found {
		return herdrObservation{State: herdrOwnershipAbsent}, nil
	}
	if workspace.WorkspaceID != expected.WorkspaceID || workspace.Label != label {
		return herdrObservation{State: herdrOwnershipMismatch}, nil
	}
	tabs, err := client.TabList(workspace.WorkspaceID)
	if err != nil {
		return herdrObservation{}, fmt.Errorf("list Herdr tabs: %w", err)
	}
	var tab herdr.Tab
	foundTab := false
	for _, candidate := range tabs {
		if candidate.TabID == expected.TabID {
			tab = candidate
			foundTab = true
			break
		}
	}
	if !foundTab {
		return herdrObservation{State: herdrOwnershipAbsent}, nil
	}
	if tab.WorkspaceID != expected.WorkspaceID || tab.Label != taskID {
		return herdrObservation{State: herdrOwnershipMismatch}, nil
	}
	pane, err := client.PaneGet(expected.PaneID)
	if err != nil {
		if isHerdrNotFound(err) {
			return herdrObservation{State: herdrOwnershipAbsent}, nil
		}
		return herdrObservation{}, fmt.Errorf("read Herdr pane: %w", err)
	}
	if pane.PaneID != expected.PaneID || pane.TabID != expected.TabID || pane.WorkspaceID != expected.WorkspaceID {
		return herdrObservation{State: herdrOwnershipMismatch}, nil
	}

	// Resource ownership ends here. Worker identity/liveness is normalized separately from the
	// persisted Attempt; pane scrollback is never promoted into generic ownership evidence.
	return herdrObservation{
		State: herdrOwnershipExact, Agent: pane.Agent, AgentStatus: pane.AgentStatus, Pane: pane,
	}, nil
}

func isHerdrNotFound(err error) bool {
	return errors.Is(err, herdr.ErrNotFound)
}

// CloseTaskTab closes a task-owned tab without touching unrelated tabs.
func CloseTaskTab(client *herdr.Client, workspaceID, tabID string) error {
	return closeTaskTab(client, workspaceID, tabID)
}

func reportCleanup(cause error, cleanupErrs ...error) error {
	cleanupErr := errors.Join(cleanupErrs...)
	if cleanupErr == nil {
		return cause
	}
	return fmt.Errorf("%w; cleanup failed: %w", cause, cleanupErr)
}
