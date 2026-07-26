// Package herdr wraps the herdr CLI's verified syntax for workspace, tab, and
// pane operations. client.go is the source of truth for that syntax, so
// SPECS.md's herdr examples should match this code, not the other way around.
package herdr

// Status is herdr's agent_status value for a pane. herdr's real vocabulary has
// five values, not four: idle, working, blocked, done, unknown.
//
// done is not a task-outcome signal. herdr derives it from its own seen/notification
// bookkeeping: a working-or-blocked pane that goes idle is reported as idle only if a
// live, OS-focused herdr client currently has that pane's tab active at the instant of
// the transition; otherwise it's reported as done. hand polls the API and never
// focuses a client on worker panes, so it observes done, essentially always, for this
// transition - never idle. Treat idle and done as the same signal ("pane stopped being
// busy") and use NotBusy to test for either.
type Status string

const (
	StatusIdle    Status = "idle"
	StatusWorking Status = "working"
	StatusBlocked Status = "blocked"
	StatusDone    Status = "done"
	StatusUnknown Status = "unknown"
)

// NotBusy reports whether status means the pane has stopped actively working or
// waiting on help - i.e. it's idle or done, herdr's two spellings of the same "not
// busy" signal. See the Status doc comment for why they must be treated as one.
func (s Status) NotBusy() bool {
	return s == StatusIdle || s == StatusDone
}

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
