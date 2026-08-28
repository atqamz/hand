// Package harness constructs the per-harness process used to launch a worker agent.
package harness

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/atqamz/hand/internal/agentsmd"
	"github.com/atqamz/hand/internal/atomicfile"
	"github.com/atqamz/hand/internal/brief"
	"github.com/atqamz/hand/internal/launch"
	"github.com/atqamz/hand/internal/shellquote"
	"github.com/atqamz/hand/internal/state"
)

const antigravityPrintTimeout = "24h"

const (
	Claude      = "claude"
	Codex       = "codex"
	Grok        = "grok"
	Pi          = "pi"
	OpenCode    = "opencode"
	Antigravity = "antigravity"
	RoleEnv     = "HAND_ROLE"
	HomeEnv     = "HAND_HOME"
	WorkerRole  = "worker"
)

// The canonical Worker Harness registry. Worker routing/configuration derive their choices from
// this list. Supervisor turn-delivery capability is independent and owned by internal/supervision;
// a product appearing here never implies it can host a Supervisor.
var names = []string{Claude, Codex, Grok, Pi, OpenCode, Antigravity}

func Names() []string {
	return slices.Clone(names)
}

func IsSupported(name string) bool {
	return slices.Contains(names, name)
}

// One interactive dialog a harness may show before it starts reading the brief; Match is checked
// against the pane's recent scrollback text. Exactly one of Keys and Refuse is set: Keys answer the
// dialog unattended, a non-empty Refuse leaves it deliberately for a human and its text says why.
type FirstRunPrompt struct {
	Name   string
	Match  *regexp.Regexp
	Keys   []string
	Refuse string
}

// A harness's verified pane signatures. Known are the dialogs whose exact wording is catalogued,
// and Unrecognized is a generic fallback for "some dialog is still on screen" that no Known entry
// matches.
type FirstRunPrompts struct {
	// The harness's own startup paint, a secondary signal that a pane herdr already reports a running
	// agent on has finished starting. A harness with no Ready signature is still confirmed, just by
	// waiting out the settle window.
	Ready        *regexp.Regexp
	Known        []FirstRunPrompt
	Unrecognized *regexp.Regexp
}

// Verified signatures per harness. Claude and codex have been observed on real first runs; every
// other harness gets the zero value until one is, leaving its launch confirmed on agent presence
// alone even if it parks on a dialog.
var firstRunPrompts = map[string]FirstRunPrompts{
	// Interactive claude gates on first-run dialogs --print skipped, and a fresh worktree path means
	// the trust one appears on every spawn, not just a fresh host.
	// internal/runtime/launch.go's confirmLaunch clears them per spawn, not leaving them for an operator to notice.
	Claude: {
		// claude's own startup paint: the splash banner, or either composer footer hint once the REPL
		// is up (the bypass-mode line replaces the shortcuts hint when the footer is wide enough).
		// None can come from the echoed launch command, so a match means claude drew a frame itself.
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
				// Nothing to do with the checked-out repo: claude's security dialog for managed
				// settings this host's org policy applies to every run. Accepting grants arbitrary code
				// execution and prompt interception - a host trust decision hand will not make for you.
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
	Codex: {
		Known: []FirstRunPrompt{
			{
				Name:   "directory trust",
				Match:  regexp.MustCompile(`Do\s+you\s+trust\s+the\s+contents\s+of\s+this\s+directory\?`),
				Refuse: "trusting this directory enables project-local config, hooks, and exec policies; hand will not accept that security decision for you; run codex yourself in this checkout once and choose whether to trust it, then respawn",
			},
		},
		Unrecognized: regexp.MustCompile(`Press\s+enter\s+to\s+continue`),
	},
}

// FirstRunPromptsFor returns name's verified first-run signatures, or the zero value if name has
// none - a known, accepted gap rather than a bug: the catalogue is what makes an unattended launch
// safe, so it matters for every harness added here, not only claude.
func FirstRunPromptsFor(name string) FirstRunPrompts {
	return firstRunPrompts[name]
}

// The harnesses whose panes herdr has been observed labeling with an agent. Codex CLI 0.146.0 was
// launched through hand, reported as codex while resident, and cleared after /quit; the false
// entries still rely only on herdr's shipped detection manifests.
var agentDetectionVerified = map[string]bool{
	Claude:   true,
	Codex:    true,
	OpenCode: true,
}

// AgentDetectionVerified reports whether herdr's agent labeling has actually been exercised
// against name. A launch that never sees an agent means something different for the two cases,
// and the failure has to say which.
func AgentDetectionVerified(name string) bool {
	return agentDetectionVerified[name]
}

type Options struct {
	Worktree            string
	Brief               string
	ReportPath          string
	Model               string
	Effort              string
	ExecutionClass      brief.ExecutionClass
	BriefHasFrontMatter bool
	// Kind is the task kind (state.KindShip or state.KindScout) briefPrompt states the launch-level
	// delivery authorization for. Empty for callers that never resolve a task, e.g. unit tests
	// exercising unrelated flags: no authorization statement is added in that case.
	Kind string
}

var modelCapable = map[string]bool{
	Claude:      true,
	Codex:       true,
	OpenCode:    true,
	Antigravity: true,
}

var effortCapable = map[string]bool{
	Claude:      true,
	Codex:       true,
	Antigravity: true,
}

// True for every supported harness: grok and pi have no verified prompt flag (atqamz/hand#418),
// so their builders append the report path and operator-decision rule to the brief file itself
// instead of passing them as a CLI argument.
var promptCapable = map[string]bool{
	Claude:      true,
	Codex:       true,
	Grok:        true,
	Pi:          true,
	OpenCode:    true,
	Antigravity: true,
}

// False means the caller must warn instead of silently dropping the model.
func SupportsModel(name string) bool {
	return modelCapable[name]
}

// False means the caller must warn instead of silently dropping the effort.
func SupportsEffort(name string) bool {
	return effortCapable[name]
}

// True regardless of delivery channel: a CLI argument for a prompt-taking harness, or an
// appendix hand writes into the brief file itself for one that only takes a file (atqamz/hand#418).
// False would mean no report channel reaches the worker at all, not merely no operator-decision rule.
func CarriesPrompt(name string) bool {
	return promptCapable[name]
}

// Build constructs the executable, arguments, environment, and working directory for a worker
// harness. Supervisor capability is a separate concern owned by internal/supervision.
func Build(name string, opts Options) (launch.LaunchSpec, error) {
	if err := ValidateEffort(name, opts.Effort); err != nil {
		return launch.LaunchSpec{}, err
	}
	var spec launch.LaunchSpec
	var buildErr error
	// Each builder below uses only flags verified against its documented CLI contract.
	switch name {
	case Claude:
		spec, buildErr = buildClaude(opts)
	case Codex:
		spec, buildErr = buildCodex(opts)
	case Grok:
		spec, buildErr = buildGrok(opts)
	case Pi:
		spec, buildErr = buildPi(opts)
	case OpenCode:
		spec, buildErr = buildOpenCode(opts)
	case Antigravity:
		spec, buildErr = buildAntigravity(opts)
	default:
		return launch.LaunchSpec{}, fmt.Errorf("harness %q not recognized", name)
	}
	if buildErr != nil {
		return launch.LaunchSpec{}, buildErr
	}
	spec.Cwd = opts.Worktree
	return launch.NewSpec(spec)
}

func buildAntigravity(o Options) (launch.LaunchSpec, error) {
	// stream-json gives launch confirmation typed init evidence without treating the provider's
	// terminal result as Hand task outcome. One-shot workers must execute unattended: otherwise a
	// permission request can soft-deny a required tool action while the headless process still exits.
	args := []string{
		"--dangerously-skip-permissions",
		"--output-format", "stream-json",
		"--print-timeout", antigravityPrintTimeout,
	}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.Effort != "" {
		args = append(args, "--effort", o.Effort)
	}
	prompt, err := briefPrompt(o)
	if err != nil {
		return launch.LaunchSpec{}, err
	}
	args = append(args, "-p", prompt)
	return launch.LaunchSpec{Executable: Executable(Antigravity), Args: args}, nil
}

// Launches claude interactively - no --print - so the pane stays resident for hand send and hand
// watch across a multi-turn no-mistakes pipeline. Verified via `claude --help`: --model, --effort
// and --dangerously-skip-permissions all apply outside --print.
func buildClaude(o Options) (launch.LaunchSpec, error) {
	// --dangerously-skip-permissions, or an unattended worker stalls on a permission prompt.
	// CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false suppresses the dim predicted-next-prompt ghost text,
	// which a pane-watching supervisor would otherwise read as typed input under an idle worker.
	args := []string{"--dangerously-skip-permissions"}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.Effort != "" {
		args = append(args, "--effort", o.Effort)
	}
	prompt, err := briefPrompt(o)
	if err != nil {
		return launch.LaunchSpec{}, err
	}
	args = append(args, prompt)
	return launch.LaunchSpec{Executable: Claude, Args: args, Env: map[string]string{"CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION": "false"}}, nil
}

// Launches Codex CLI 0.146.0 interactively with its positional prompt. Its help and config schema
// expose the flags below; paste-burst buffering otherwise absorbs hand send's immediate Enter, and
// auto effort means inherit Codex's default rather than pass a literal value.
func buildCodex(o Options) (launch.LaunchSpec, error) {
	args := []string{"--dangerously-bypass-approvals-and-sandbox", "-c", "disable_paste_burst=true"}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.Effort != "" && o.Effort != "auto" {
		args = append(args, "-c", fmt.Sprintf(`model_reasoning_effort="%s"`, o.Effort))
	}
	prompt, err := briefPrompt(o)
	if err != nil {
		return launch.LaunchSpec{}, err
	}
	args = append(args, prompt)
	return launch.LaunchSpec{Executable: Codex, Args: args}, nil
}

// Neither takes a prompt argument: AppendPromptToBrief carries the launch statement instead,
// called once by the provisioning path before Build runs, so Build stays a pure function from
// Options to a LaunchSpec (atqamz/hand#418).
func buildGrok(o Options) (launch.LaunchSpec, error) {
	return launch.LaunchSpec{Executable: Grok, Args: []string{"--trust", "--file", o.Brief}}, nil
}

func buildPi(o Options) (launch.LaunchSpec, error) {
	return launch.LaunchSpec{Executable: Pi, Args: []string{o.Brief}}, nil
}

// Uses the bare `opencode` command (verified via `opencode --help`), which opens an interactive TUI,
// rather than `opencode run` - that one is explicitly headless and exits after a single reply.
func buildOpenCode(o Options) (launch.LaunchSpec, error) {
	// OPENCODE_CONFIG_CONTENT grants blanket tool permission so an unattended worker does not stall
	// on a permission prompt.
	args := []string{}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	// The bare command has no --file flag and no effort or variant flag, so the brief path rides in
	// the --prompt text and Options.Effort is dropped here rather than passed.
	prompt, err := briefPrompt(o)
	if err != nil {
		return launch.LaunchSpec{}, err
	}
	args = append(args, "--prompt", prompt)
	return launch.LaunchSpec{Executable: OpenCode, Args: args, Env: map[string]string{"OPENCODE_CONFIG_CONTENT": `{"permission":{"*":"allow"}}`}}, nil
}

// Shared by every harness regardless of delivery channel, so the report path and the
// operator-decision rule cannot drift into two wordings (atqamz/hand#418). Ends with
// agentsmd.OperatorDecisionRule: a worktree is never under the fleet home, so this is the only channel that rule has.
func launchStatement(o Options) (string, error) {
	if o.ReportPath == "" {
		return "", fmt.Errorf("report path is required for a prompt-capable harness")
	}
	statement := fmt.Sprintf("The worker report channel is %s. Append every state change to that file with plain shell redirection; this is the only way anything you say reaches the supervisor. Use these report prefixes: working:, done:, failed:, blocked:, needs-decision:, paused:.", shellquote.Quote(o.ReportPath))
	switch o.Kind {
	case state.KindShip:
		statement += " You are authorized to commit, push your branch, and open the pull request; merging and closing the issue are the supervisor's action only."
	case state.KindScout:
		statement += " Your deliverable is a report; you must not commit, push, or open a pull request."
	}
	if o.BriefHasFrontMatter {
		statement += " Any model, effort, execution_class, or planned_against keys in its leading '---' block are dispatch metadata, not task instructions."
	}
	if o.ExecutionClass == brief.ExecutionClassMechanical {
		statement += " Verify the named files/symbols and plan assumptions before editing. If materially stale or contradictory, stop and report blocked. Do not redesign the task yourself. Otherwise execute the ordered plan and verification steps."
	}
	return statement + " " + agentsmd.OperatorDecisionRule, nil
}

// The CLI-argument delivery of launchStatement, used by every harness that takes a prompt
// flag or positional argument instead of only a brief file path.
func briefPrompt(o Options) (string, error) {
	statement, err := launchStatement(o)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Read the brief at %s and carry out the task it describes.", o.Brief) + " " + statement, nil
}

// AppendPromptToBrief is grok's and pi's delivery of launchStatement, a no-op for every other
// harness (atqamz/hand#418). Called once by the provisioning path before Build runs - never to
// reconstruct already-persisted launch evidence, which must stay a read.
func AppendPromptToBrief(name string, o Options) error {
	switch name {
	case Grok, Pi:
	default:
		return nil
	}
	return appendLaunchStatement(o)
}

// The marker line keeps the appendix visibly hand's text rather than something the supervisor's
// brief said.
func appendLaunchStatement(o Options) error {
	statement, err := launchStatement(o)
	if err != nil {
		return err
	}
	info, err := os.Stat(o.Brief)
	if err != nil {
		return fmt.Errorf("stat brief for launch statement: %w", err)
	}
	data, err := os.ReadFile(o.Brief)
	if err != nil {
		return fmt.Errorf("read brief for launch statement: %w", err)
	}
	// Also gates the append: a brief already carrying the marker (a resumed or reopened attempt
	// re-provisioning the same file) is left alone rather than growing a second copy.
	if strings.Contains(string(data), brief.AppendMarker) {
		return nil
	}
	appendix := fmt.Sprintf("\n\n---\n\n%s\n\n%s\n", brief.AppendMarker, statement)
	if err := atomicfile.Write(o.Brief, ".brief-append-", append(data, appendix...), info.Mode().Perm()); err != nil {
		return fmt.Errorf("append launch statement to brief: %w", err)
	}
	return nil
}
