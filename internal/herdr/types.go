// Package herdr wraps the herdr CLI's verified workspace, tab, and pane syntax.
// client.go and internal/faketool/FIDELITY.md own the calls and observed shapes.
package herdr

import (
	"path/filepath"
	"strings"
)

// Status is herdr's agent_status value for a pane. The real vocabulary has five values, not four:
// idle, working, blocked, done, unknown - and idle and done are one signal, "the pane stopped
// being busy", which NotBusy is the way to test for.
type Status string

const (
	StatusIdle    Status = "idle"
	StatusWorking Status = "working"
	StatusBlocked Status = "blocked"
	// Not a task outcome: herdr derives it from its own seen/notification bookkeeping, reporting a
	// working-or-blocked pane that goes idle as idle only while a live, OS-focused client has that
	// pane's tab active. hand polls headlessly, so it observes done for that transition, not idle.
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

type Process struct {
	PID     int      `json:"pid"`
	Name    string   `json:"name"`
	Argv    []string `json:"argv"`
	Argv0   string   `json:"argv0"`
	Cmdline string   `json:"cmdline"`
	Cwd     string   `json:"cwd"`
}

type ProcessInfo struct {
	PaneID                   string    `json:"pane_id"`
	ShellPID                 int       `json:"shell_pid"`
	ForegroundProcessGroupID int       `json:"foreground_process_group_id"`
	TTY                      string    `json:"tty"`
	ForegroundProcesses      []Process `json:"foreground_processes"`
}

func (p ProcessInfo) HasExecutable(executable string) bool {
	executable = processBase(executable)
	for _, process := range p.ForegroundProcesses {
		if len(process.Argv) > 0 && processBase(process.Argv[0]) == executable {
			return true
		}
		if processBase(process.Name) == executable || processBase(process.Argv0) == executable {
			return true
		}
	}
	return false
}

func processBase(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	return strings.TrimPrefix(filepath.Base(value), "-")
}

// Agent names the harness herdr detects in the pane, empty when the pane holds no agent - a bare
// shell, or one whose harness exited. AgentStatus is "unknown" then, but also for a pane herdr has
// not classified yet, so Agent is the field to test for a running harness.
type Pane struct {
	PaneID        string `json:"pane_id"`
	TabID         string `json:"tab_id"`
	WorkspaceID   string `json:"workspace_id"`
	Agent         string `json:"agent"`
	AgentStatus   Status `json:"agent_status"`
	Cwd           string `json:"cwd"`
	ForegroundCwd string `json:"foreground_cwd"`
}
