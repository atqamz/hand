package herdr

import (
	"fmt"
	"path/filepath"

	handgit "github.com/atqamz/hand/internal/git"
	"github.com/atqamz/hand/internal/launch"
)

// PaneRunExecGuard types only `<hand> exec-guard <locator>` into the pane shell, once the pane
// shell alone holds the foreground in cwd. The harness spec, its environment and every secret
// stay in the handoff. Errors before pane run are marked process-not-started.
func (c *Client) PaneRunExecGuard(paneID, cwd, hand, locator string) error {
	if !filepath.IsAbs(hand) || !filepath.IsAbs(locator) {
		return &ExecError{Started: false, Err: fmt.Errorf("exec guard invocation needs an absolute hand %q and handoff locator %q", hand, locator)}
	}
	spec, err := launch.NewSpec(launch.LaunchSpec{Executable: hand, Args: []string{"exec-guard", locator}, Cwd: cwd})
	if err != nil {
		return &ExecError{Started: false, Err: fmt.Errorf("validate exec guard invocation: %w", err)}
	}
	info, err := c.PaneProcessInfo(paneID)
	if err != nil {
		return &ExecError{Started: false, Err: fmt.Errorf("observe pane shell: %w", err)}
	}
	shell, err := shellForProcess(info)
	if err != nil {
		return &ExecError{Started: false, Err: err}
	}
	if info.ForegroundProcessGroupID != info.ShellPID {
		return &ExecError{Started: false, Err: fmt.Errorf("refuse exact launch: foreground process group %d does not match shell pid %d", info.ForegroundProcessGroupID, info.ShellPID)}
	}
	if len(info.ForegroundProcesses) != 1 || info.ForegroundProcesses[0].PID != info.ShellPID {
		return &ExecError{Started: false, Err: fmt.Errorf("refuse exact launch: foreign foreground process exists")}
	}
	if info.ForegroundProcesses[0].Cwd == "" {
		return &ExecError{Started: false, Err: fmt.Errorf("refuse exact launch: missing shell cwd")}
	}
	if !handgit.SamePath(info.ForegroundProcesses[0].Cwd, spec.Cwd) {
		return &ExecError{Started: false, Err: fmt.Errorf("refuse exact launch: shell cwd %q does not match launch cwd %q", info.ForegroundProcesses[0].Cwd, spec.Cwd)}
	}
	render := renderPOSIX
	if shell == shellPowerShell {
		render = renderPowerShell
	}
	command, err := render(spec.Executable, spec.Args)
	if err != nil {
		return &ExecError{Started: false, Err: fmt.Errorf("render exec guard invocation for %s: %w", shell, err)}
	}
	return c.paneRun(paneID, command)
}
