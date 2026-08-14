package runtime

import (
	"errors"
	"fmt"

	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
)

type herdrClient interface {
	FindWorkspaceByLabel(string) (herdr.Workspace, bool, error)
	WorkspaceCreate(string, string) (herdr.Workspace, herdr.Tab, herdr.Pane, error)
	WorkspaceClose(string) error
	TabList(string) ([]herdr.Tab, error)
	TabCreate(string, string, string) (herdr.Tab, herdr.Pane, error)
	TabRename(string, string) error
	TabClose(string) error
	PaneGet(string) (herdr.Pane, error)
	PaneRun(string, string) error
	PaneSendKeys(string, ...string) error
	PaneRead(string, int) (string, error)
}

func newHerdrClient() herdrClient { return herdr.NewClient() }

func herdrWorkspaceLabel(projectName string) string { return "hand:" + projectName }

func acquireTaskWorkspace(client herdrClient, worktreePath, taskID, projectName string) (herdr.Workspace, herdr.Tab, herdr.Pane, func() error, error) {
	label := herdrWorkspaceLabel(projectName)
	workspace, found, err := client.FindWorkspaceByLabel(label)
	createdWorkspace := false
	var rootTab herdr.Tab
	var rootPane herdr.Pane
	if err == nil && !found {
		workspace, rootTab, rootPane, err = client.WorkspaceCreate(worktreePath, label)
		createdWorkspace = err == nil
	}
	if err != nil {
		return herdr.Workspace{}, herdr.Tab{}, herdr.Pane{}, nil, fmt.Errorf("herdr workspace lookup/create failed: %w", err)
	}

	tab, pane, err := acquireTaskTab(client, createdWorkspace, workspace.WorkspaceID, worktreePath, taskID, rootTab, rootPane)
	if err != nil {
		return herdr.Workspace{}, herdr.Tab{}, herdr.Pane{}, nil, reportCleanup(err, rollbackHerdr(client, createdWorkspace, workspace.WorkspaceID, ""))
	}
	rollback := func() error {
		return rollbackHerdr(client, createdWorkspace, workspace.WorkspaceID, tab.TabID)
	}
	return workspace, tab, pane, rollback, nil
}

func acquireTaskTab(client herdrClient, createdWorkspace bool, workspaceID, worktreePath, taskID string, rootTab herdr.Tab, rootPane herdr.Pane) (herdr.Tab, herdr.Pane, error) {
	if createdWorkspace {
		if err := client.TabRename(rootTab.TabID, taskID); err != nil {
			return herdr.Tab{}, herdr.Pane{}, fmt.Errorf("herdr tab rename failed: %w", err)
		}
		return rootTab, rootPane, nil
	}
	tab, pane, err := client.TabCreate(workspaceID, worktreePath, taskID)
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

func incompleteHerdrOwnership(ownership state.Herdr) error {
	if ownership.WorkspaceID == "" && ownership.TabID == "" && ownership.PaneID == "" {
		return nil
	}
	if ownership.WorkspaceID == "" || ownership.TabID == "" {
		return fmt.Errorf("workspace and tab identity are incomplete (workspace=%q, tab=%q, pane=%q)", ownership.WorkspaceID, ownership.TabID, ownership.PaneID)
	}
	return nil
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
