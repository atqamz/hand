// Package harness constructs the per-harness command used to launch a worker agent.
package harness

import (
	"fmt"
	"strings"
)

const (
	Claude   = "claude"
	Codex    = "codex"
	Grok     = "grok"
	Pi       = "pi"
	OpenCode = "opencode"
)

var supported = map[string]bool{
	Claude:   true,
	Codex:    true,
	Grok:     true,
	Pi:       true,
	OpenCode: true,
}

func IsSupported(name string) bool {
	return supported[name]
}

type Options struct {
	Worktree string
	Brief    string
	Model    string
	Effort   string
}

// Build constructs the shell command that cds into the worktree and launches the harness
// against the brief. Flags verified against the installed CLI (--help) are used where
// available; unverified harnesses fall back to the SPECS.md template syntax.
func Build(name string, opts Options) (string, error) {
	var launch string
	switch name {
	case Claude:
		launch = buildClaude(opts)
	case Codex:
		launch = buildCodex(opts)
	case Grok:
		launch = buildGrok(opts)
	case Pi:
		launch = buildPi(opts)
	case OpenCode:
		launch = buildOpenCode(opts)
	default:
		return "", fmt.Errorf("harness %q not recognized", name)
	}
	return fmt.Sprintf("cd %s && %s", shellQuote(opts.Worktree), launch), nil
}

// buildClaude reads the brief itself: claude --print takes the prompt as a positional
// argument, not a file path (verified via `claude --help`), so the brief-path template
// in SPECS.md would otherwise send the literal path string as the prompt text.
func buildClaude(o Options) string {
	args := []string{"claude", "--print"}
	if o.Model != "" {
		args = append(args, "--model", shellQuote(o.Model))
	}
	if o.Effort != "" {
		args = append(args, "--effort", shellQuote(o.Effort))
	}
	prompt := fmt.Sprintf("Read the brief at %s and carry out the task it describes.", o.Brief)
	args = append(args, shellQuote(prompt))
	return strings.Join(args, " ")
}

func buildCodex(o Options) string {
	return fmt.Sprintf("codex --file %s", shellQuote(o.Brief))
}

func buildGrok(o Options) string {
	return fmt.Sprintf("grok --trust --file %s", shellQuote(o.Brief))
}

func buildPi(o Options) string {
	return fmt.Sprintf("pi %s", shellQuote(o.Brief))
}

// buildOpenCode uses `opencode run` (verified via `opencode --help`) rather than the bare
// `opencode` template in SPECS.md, which would open an interactive TUI instead of running
// headless. --file attaches the brief to the initial message.
func buildOpenCode(o Options) string {
	args := []string{"opencode", "run", "--file", shellQuote(o.Brief)}
	if o.Model != "" {
		args = append(args, "--model", shellQuote(o.Model))
	}
	if o.Effort != "" {
		args = append(args, "--variant", shellQuote(o.Effort))
	}
	args = append(args, shellQuote("Follow the attached brief and complete the task."))
	return strings.Join(args, " ")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
