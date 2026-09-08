package herdr

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/atqamz/hand/internal/launch"
	"github.com/atqamz/hand/internal/shellquote"
)

// PaneRunCanonicalSpec starts one structured canonical worker process only from
// an exact idle shell whose observed cwd still matches the persisted LaunchSpec.
func (c *Client) PaneRunCanonicalSpec(paneID string, spec launch.LaunchSpec) error {
	if err := spec.Validate(); err != nil {
		return fmt.Errorf("validate canonical launch spec: %w", err)
	}
	info, err := c.PaneProcessInfo(paneID)
	if err != nil {
		return fmt.Errorf("observe canonical launch pane process info: %w", err)
	}
	shell, err := shellForProcess(info)
	if err != nil {
		return fmt.Errorf("observe canonical launch shell: %w", err)
	}
	if info.ForegroundProcessGroupID != info.ShellPID {
		return fmt.Errorf("canonical launch pane is not idle: foreground process group %d, shell pid %d", info.ForegroundProcessGroupID, info.ShellPID)
	}
	var shellProcess *Process
	for i := range info.ForegroundProcesses {
		process := &info.ForegroundProcesses[i]
		if process.PID == info.ShellPID {
			if shellProcess != nil {
				return fmt.Errorf("canonical launch pane reports shell pid %d more than once", info.ShellPID)
			}
			shellProcess = process
			continue
		}
		return fmt.Errorf("canonical launch pane has non-shell foreground process %d before mutation", process.PID)
	}
	if shellProcess == nil {
		return fmt.Errorf("canonical launch pane has no foreground process for shell pid %d", info.ShellPID)
	}
	if shellProcess.Cwd == "" || !sameCanonicalLaunchPath(shellProcess.Cwd, spec.Cwd) {
		return fmt.Errorf("canonical launch shell cwd %q does not match persisted cwd %q", shellProcess.Cwd, spec.Cwd)
	}

	var command string
	switch shell {
	case shellPOSIX:
		command, err = renderCanonicalPOSIXLaunch(spec)
	case shellPowerShell:
		command, err = renderCanonicalPowerShellLaunch(spec)
	default:
		err = fmt.Errorf("unsupported canonical launch shell %q", shell)
	}
	if err != nil {
		return err
	}
	return c.paneRun(paneID, command)
}

func renderCanonicalPOSIXLaunch(spec launch.LaunchSpec) (string, error) {
	command, err := renderPOSIX(spec.Executable, spec.Args)
	if err != nil {
		return "", err
	}
	keys := canonicalLaunchEnvironmentKeys(spec.Env)
	parts := make([]string, 0, len(keys)+1)
	for _, key := range keys {
		value := spec.Env[key]
		if err := validateShellValue("environment value for "+key, value); err != nil {
			return "", err
		}
		parts = append(parts, key+"="+shellquote.Quote(value))
	}
	parts = append(parts, command)
	return joinShellParts(parts), nil
}

func renderCanonicalPowerShellLaunch(spec launch.LaunchSpec) (string, error) {
	command, err := renderPowerShell(spec.Executable, spec.Args)
	if err != nil {
		return "", err
	}
	keys := canonicalLaunchEnvironmentKeys(spec.Env)
	parts := make([]string, 0, len(keys)+1)
	for _, key := range keys {
		value := spec.Env[key]
		if err := validateShellValue("environment value for "+key, value); err != nil {
			return "", err
		}
		parts = append(parts, "$env:"+key+"="+powerShellQuote(value)+";")
	}
	parts = append(parts, command)
	return joinShellParts(parts), nil
}

func canonicalLaunchEnvironmentKeys(environment map[string]string) []string {
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sameCanonicalLaunchPath(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
