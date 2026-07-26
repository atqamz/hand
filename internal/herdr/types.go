// Package herdr wraps the herdr CLI's verified syntax for workspace, tab, and
// pane operations. client.go is the source of truth for that syntax, so
// SPECS.md's herdr examples should match this code, not the other way around.
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

// Agent names the harness herdr detects running in the pane, and is empty when the pane holds
// no agent - a bare shell, or one whose harness has exited. AgentStatus is "unknown" in that
// case, but it is also "unknown" for a pane herdr has not classified yet, so Agent is the field
// to test for a running harness.
type Pane struct {
	PaneID      string `json:"pane_id"`
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Agent       string `json:"agent"`
	AgentStatus Status `json:"agent_status"`
}
