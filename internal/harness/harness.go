// Package harness constructs the per-harness command used to launch a worker agent.
package harness

import (
	"fmt"
	"regexp"
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

// FirstRunPrompt is one interactive dialog a harness may show before it starts reading the
// brief, along with the keys that answer it. Match is checked against the pane's recent
// scrollback text.
type FirstRunPrompt struct {
	Name  string
	Match *regexp.Regexp
	Keys  []string
}

// FirstRunPrompts is a harness's known first-run dialogs plus a generic fallback pattern for
// recognizing "some dialog is still on screen" even when it doesn't match any Known entry.
// Unrecognized being nil means any non-blank pane content is treated as a possible unanswered
// dialog until it goes quiet - the safe default for a harness with no verified signatures.
type FirstRunPrompts struct {
	Known        []FirstRunPrompt
	Unrecognized *regexp.Regexp
}

// firstRunPrompts holds verified signatures per harness. Only claude has been verified against
// a real first run (see cmd/launch.go); every other harness gets the empty value, which
// confirmLaunch treats as "confirm the pane goes quiet, answer nothing."
var firstRunPrompts = map[string]FirstRunPrompts{
	Claude: {
		Known: []FirstRunPrompt{
			{
				Name:  "workspace trust",
				Match: regexp.MustCompile(`Yes,\s+I\s+trust\s+this\s+folder`),
				Keys:  []string{"Enter"},
			},
			{
				// Defaults focus to "No, exit" - a blind Enter declines it, so this needs Down
				// first to reach "Yes, I accept" before confirming.
				Name:  "bypass permissions",
				Match: regexp.MustCompile(`Bypass\s+Permissions\s+mode`),
				Keys:  []string{"Down", "Enter"},
			},
		},
		// Both dialogs above end in this footer, wrapped or not; a harness update that
		// reshuffles their wording still trips this fallback instead of confirmLaunch mistaking
		// the dialog for a finished worker.
		Unrecognized: regexp.MustCompile(`Enter\s+to\s+confirm`),
	},
}

// FirstRunPromptsFor returns name's known first-run dialogs, or the empty value (answer
// nothing, just confirm the pane goes quiet) if name has none verified.
func FirstRunPromptsFor(name string) FirstRunPrompts {
	return firstRunPrompts[name]
}

type Options struct {
	Worktree string
	Brief    string
	Model    string
	Effort   string
}

// Build constructs the shell command that cds into the worktree and launches the harness
// against the brief. Every launch must be interactive, not one-shot: hand send steers a
// running pane, hand watch classifies its lifecycle, and a no-mistakes pipeline drives many
// turns, none of which a one-shot process can do. Flags verified against the installed CLI
// (--help) are used where available - claude and opencode - and this file is the source of
// truth for those two. Codex, Grok, and Pi have no binary available yet, so they fall back to
// the SPECS.md template syntax and must be re-verified for interactive launch, not just
// flag names, once installable.
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

// buildClaude launches claude interactively (no --print) so the pane stays resident for
// hand send and hand watch across a multi-turn no-mistakes pipeline (verified via
// `claude --help`: --model, --effort, and --dangerously-skip-permissions all apply outside
// --print). --dangerously-skip-permissions is required or an unattended worker stalls on a
// permission prompt. CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false suppresses claude's dim
// predicted-next-prompt ghost text, which would otherwise read to a pane-watching supervisor
// as the worker having typed input while actually idle. Interactive claude also gates on two
// first-run dialogs that --print skipped (workspace trust, bypass-permissions disclaimer); both
// are one-time host setup, documented under Harness launch templates in SPECS.md. Their
// signatures live in firstRunPrompts above, and cmd/launch.go's confirmLaunch clears them
// after every spawn/promote instead of leaving them for an operator to notice and answer.
func buildClaude(o Options) string {
	args := []string{"CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false", "claude", "--dangerously-skip-permissions"}
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

// buildOpenCode uses the bare `opencode` command (verified via `opencode --help`), which opens
// an interactive TUI, rather than `opencode run`, which is explicitly headless and exits after
// one reply. OPENCODE_CONFIG_CONTENT grants blanket tool permission so an unattended worker
// does not stall on a permission prompt. The bare command has no --file flag and no
// effort/variant flag, so the brief path is embedded in --prompt text instead, and Effort is
// not applied here.
func buildOpenCode(o Options) string {
	args := []string{"OPENCODE_CONFIG_CONTENT=" + shellQuote(`{"permission":{"*":"allow"}}`), "opencode"}
	if o.Model != "" {
		args = append(args, "--model", shellQuote(o.Model))
	}
	prompt := fmt.Sprintf("Read the brief at %s and carry out the task it describes.", o.Brief)
	args = append(args, "--prompt", shellQuote(prompt))
	return strings.Join(args, " ")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
