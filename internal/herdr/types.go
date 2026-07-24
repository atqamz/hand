// Package herdr wraps the herdr CLI for workspace, tab, and pane operations.
package herdr

type Status string

const (
	StatusIdle    Status = "idle"
	StatusWorking Status = "working"
	StatusBlocked Status = "blocked"
	StatusDone    Status = "done"
	StatusUnknown Status = "unknown"
)

type Workspace struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	TabCount    int    `json:"tab_count"`
}

type Tab struct {
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}

type Pane struct {
	PaneID      string `json:"pane_id"`
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	AgentStatus Status `json:"agent_status"`
}
