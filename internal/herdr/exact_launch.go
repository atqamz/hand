package herdr

import (
	"fmt"
	"sort"
	"strings"

	"github.com/atqamz/hand/internal/launch"
	"github.com/atqamz/hand/internal/shellquote"
)

// PaneRunExactSpec starts one exact structured process with explicit LaunchSpec cwd and environment.
// Unlike transitional PaneRunSpec, it does not inherit semantic child settings from daemon/pane state.
// Errors before pane run are marked process-not-started.
func (c *Client) PaneRunExactSpec(paneID string, spec launch.LaunchSpec) error {
	if err := spec.Validate(); err != nil {
		return &ExecError{Started: false, Err: fmt.Errorf("validate exact launch spec: %w", err)}
	}
	info, err := c.PaneProcessInfo(paneID)
	if err != nil {
		return &ExecError{Started: false, Err: fmt.Errorf("observe pane shell: %w", err)}
	}
	shell, err := shellForProcess(info)
	if err != nil {
		return &ExecError{Started: false, Err: err}
	}
	command, err := renderExactLaunchSpec(shell, spec)
	if err != nil {
		return &ExecError{Started: false, Err: fmt.Errorf("render exact launch for %s: %w", shell, err)}
	}
	return c.paneRun(paneID, command)
}

func renderExactLaunchSpec(shell shellFamily, spec launch.LaunchSpec) (string, error) {
	if err := spec.Validate(); err != nil {
		return "", err
	}
	switch shell {
	case shellPOSIX:
		return renderExactPOSIXLaunchSpec(spec)
	case shellPowerShell:
		return renderExactPowerShellLaunchSpec(spec)
	default:
		return "", fmt.Errorf("unsupported shell %q", shell)
	}
}

func renderExactPOSIXLaunchSpec(spec launch.LaunchSpec) (string, error) {
	command, err := renderPOSIX(spec.Executable, spec.Args)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(spec.Env)+1)
	for _, name := range sortedLaunchEnvironmentNames(spec.Env) {
		parts = append(parts, name+"="+shellquote.Quote(spec.Env[name]))
	}
	parts = append(parts, command)
	return "cd -- " + shellquote.Quote(spec.Cwd) + " && " + joinShellParts(parts), nil
}

func renderExactPowerShellLaunchSpec(spec launch.LaunchSpec) (string, error) {
	command, err := renderPowerShell(spec.Executable, spec.Args)
	if err != nil {
		return "", err
	}
	names := sortedLaunchEnvironmentNames(spec.Env)
	var b strings.Builder
	b.WriteString("& { $handOldLocation = Get-Location; $handOldEnv = @{}; try { ")
	for _, name := range names {
		quotedName := powerShellQuote(name)
		b.WriteString("$handOldEnv[")
		b.WriteString(quotedName)
		b.WriteString("] = [Environment]::GetEnvironmentVariable(")
		b.WriteString(quotedName)
		b.WriteString(", 'Process'); [Environment]::SetEnvironmentVariable(")
		b.WriteString(quotedName)
		b.WriteString(", ")
		b.WriteString(powerShellQuote(spec.Env[name]))
		b.WriteString(", 'Process'); ")
	}
	b.WriteString("Set-Location -LiteralPath ")
	b.WriteString(powerShellQuote(spec.Cwd))
	b.WriteString("; ")
	b.WriteString(command)
	b.WriteString(" } finally { Set-Location -LiteralPath $handOldLocation.Path; ")
	for _, name := range names {
		quotedName := powerShellQuote(name)
		b.WriteString("if ($null -eq $handOldEnv[")
		b.WriteString(quotedName)
		b.WriteString("]) { [Environment]::SetEnvironmentVariable(")
		b.WriteString(quotedName)
		b.WriteString(", $null, 'Process') } else { [Environment]::SetEnvironmentVariable(")
		b.WriteString(quotedName)
		b.WriteString(", $handOldEnv[")
		b.WriteString(quotedName)
		b.WriteString("], 'Process') }; ")
	}
	b.WriteString("} }")
	return b.String(), nil
}

func sortedLaunchEnvironmentNames(environment map[string]string) []string {
	names := make([]string, 0, len(environment))
	for name := range environment {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
