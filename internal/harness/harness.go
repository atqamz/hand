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
// brief. Match is checked against the pane's recent scrollback text. Exactly one of Keys and
// Refuse is set: Keys answer the dialog unattended, while a non-empty Refuse marks a dialog
// that is recognized but deliberately left for a human, and its text says why.
type FirstRunPrompt struct {
	Name   string
	Match  *regexp.Regexp
	Keys   []string
	Refuse string
}

// FirstRunPrompts is a harness's verified pane signatures. Known are the dialogs whose exact
// wording is catalogued, and Unrecognized is a generic fallback for "some dialog is still on
// screen" that no Known entry matches. Ready is the harness's own startup paint, a secondary
// signal that a pane herdr already reports a running agent on has finished starting; a harness
// with no Ready signature is still confirmed, just by waiting out the settle window. A zero
// value leaves the launch confirmed on agent presence alone, so a harness with no catalogued
// signatures that parks on a dialog is still reported as started - a known, accepted gap, and
// the reason the catalogue matters for every harness added here, not only claude.
type FirstRunPrompts struct {
	Ready        *regexp.Regexp
	Known        []FirstRunPrompt
	Unrecognized *regexp.Regexp
}

// firstRunPrompts holds verified signatures per harness. Only claude has been verified against
// a real first run (see cmd/launch.go); every other harness gets the zero value until one is.
var firstRunPrompts = map[string]FirstRunPrompts{
	Claude: {
		// claude's own startup paint: the splash banner, or one of the two composer footer hints
		// once the REPL is up (the bypass-mode line replaces the shortcuts hint whenever the
		// footer is wide enough to show it). None of these can come from the echoed launch
		// command, so matching one means claude itself drew a frame.
		Ready: regexp.MustCompile(`Welcome\s+to\s+Claude\s+Code|\?\s+for\s+shortcuts|bypass\s+permissions\s+on`),
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
			{
				// Nothing to do with the checked-out repo: this is claude's security dialog for
				// managed settings this host's organization policy applies to every run, and
				// accepting it grants arbitrary code execution and prompt interception. That is
				// a trust decision about the host, so hand will not make it for the operator.
				Name:   "managed settings",
				Match:  regexp.MustCompile(`Managed\s+settings\s+require\s+approval|Yes,\s+I\s+trust\s+these\s+settings`),
				Refuse: "this host has managed settings claude requires approval for, which hand will not accept for you; run claude yourself on this host once and accept the managed-settings prompt, then respawn",
			},
		},
		// Every dialog above ends in this footer, wrapped or not; a harness update that
		// reshuffles their wording still trips this fallback instead of confirmLaunch mistaking
		// the dialog for a started worker.
		Unrecognized: regexp.MustCompile(`Enter\s+to\s+confirm`),
	},
}

// FirstRunPromptsFor returns name's verified first-run signatures, or the zero value if name
// has none.
func FirstRunPromptsFor(name string) FirstRunPrompts {
	return firstRunPrompts[name]
}

// agentDetectionVerified lists the harnesses whose panes herdr has been observed labeling with
// an agent, by running the real binary in a real pane. The others ship a detection manifest
// under herdr's agent-detection state dir, read but never exercised here because no binary for
// them is installed on this host.
var agentDetectionVerified = map[string]bool{
	Claude:   true,
	OpenCode: true,
}

// AgentDetectionVerified reports whether herdr's agent labeling has actually been exercised
// against name. A launch that never sees an agent means something different for the two cases,
// and the failure has to say which.
func AgentDetectionVerified(name string) bool {
	return agentDetectionVerified[name]
}

type Options struct {
	Worktree string
	Brief    string
	Model    string
	Effort   string
	// FrontMatter: the worker reads Brief itself, so the prompt has to disclaim this block.
	FrontMatter bool
}

var effortCapable = map[string]bool{
	Claude: true,
}

// SupportsEffort: false means the caller must warn instead of silently dropping the effort.
func SupportsEffort(name string) bool {
	return effortCapable[name]
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
// as the worker having typed input while actually idle. Interactive claude also gates on
// first-run dialogs that --print skipped (workspace trust, bypass-permissions disclaimer), and
// a fresh worktree path means the trust one appears on every spawn, not just on a fresh host -
// documented under Harness launch templates in SPECS.md. Their signatures live in
// firstRunPrompts above, and cmd/launch.go's confirmLaunch clears them after every
// spawn/promote instead of leaving them for an operator to notice and answer.
func buildClaude(o Options) string {
	args := []string{"CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false", "claude", "--dangerously-skip-permissions"}
	if o.Model != "" {
		args = append(args, "--model", shellQuote(o.Model))
	}
	if o.Effort != "" {
		args = append(args, "--effort", shellQuote(o.Effort))
	}
	args = append(args, shellQuote(briefPrompt(o)))
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
	args = append(args, "--prompt", shellQuote(briefPrompt(o)))
	return strings.Join(args, " ")
}

// briefPrompt is shared so the wording cannot drift between harnesses.
func briefPrompt(o Options) string {
	prompt := fmt.Sprintf("Read the brief at %s and carry out the task it describes.", o.Brief)
	if o.FrontMatter {
		prompt += " Its leading '---' front matter is dispatch metadata (model/effort selection), not task content; skip past it."
	}
	return prompt
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
