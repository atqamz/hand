package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/agentsmd"
	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/git"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/integration"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/registry"
	"github.com/atqamz/hand/internal/routing"
	"github.com/atqamz/hand/internal/selfupdate"
	"github.com/atqamz/hand/internal/skill"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/supervision"
	"github.com/atqamz/hand/internal/toolchain"
	"github.com/atqamz/hand/internal/worktree"
	"github.com/spf13/cobra"
)

// Every fleet needs these regardless of which projects are registered, so bootstrap may install
// them with consent; gh and a coding-agent harness are checked separately below, since the
// former is only required by some project delivery modes and hand never picks the latter.
var foundationalTools = []string{"git", "treehouse", "herdr"}

type doctorSeverity string

const (
	doctorError   doctorSeverity = "error"
	doctorWarning doctorSeverity = "warning"
	doctorInfo    doctorSeverity = "info"
)

type doctorFinding struct {
	Line     int
	Severity doctorSeverity
	Text     string
}

var doctorFields = []axi.Column[doctorFinding]{
	{Name: "line", Value: func(f doctorFinding) string {
		if f.Line == 0 {
			return "none"
		}
		return strconv.Itoa(f.Line)
	}},
	{Name: "severity", Value: func(f doctorFinding) string { return string(f.Severity) }},
	{Name: "finding", Value: func(f doctorFinding) string { return f.Text }},
}

var doctorDefaultFields = []string{"line", "severity", "finding"}

// This is the readiness contract a bootstrapper, a human and a supervising agent all read off
// the same `hand doctor` output instead of any of them inventing a second schema.
type toolReadiness struct {
	Tool      string
	Installed bool
	Required  bool
}

type harnessReadiness struct {
	Name      string
	Installed bool
}

var toolReadinessFields = []axi.Column[toolReadiness]{
	{Name: "tool", Value: func(t toolReadiness) string { return t.Tool }},
	{Name: "installed", Value: func(t toolReadiness) string { return strconv.FormatBool(t.Installed) }},
	{Name: "required", Value: func(t toolReadiness) string { return strconv.FormatBool(t.Required) }},
}

var harnessReadinessFields = []axi.Column[harnessReadiness]{
	{Name: "name", Value: func(h harnessReadiness) string { return h.Name }},
	{Name: "installed", Value: func(h harnessReadiness) string { return strconv.FormatBool(h.Installed) }},
}

func newDoctorCmd(info selfupdate.BuildInfo) *cobra.Command {
	var fields []string

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check fleet health: AGENTS.md, the bundled skill, project gates, routing, and tools",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			histories, err := state.ListOpenHistoriesReadOnly(fleetHome)
			if err != nil {
				return err
			}
			findings, err := doctorFindings(fleetHome, histories)
			if err != nil {
				return err
			}
			cols, err := pickFields(doctorFields, fields, doctorDefaultFields)
			if err != nil {
				return err
			}

			path := filepath.Join(fleetHome, "AGENTS.md")
			failing := 0
			for _, finding := range findings {
				if finding.Severity == doctorError {
					failing++
				}
			}

			projects, err := project.ListReadOnly(fleetHome)
			if err != nil {
				return err
			}
			harnesses := doctorHarnesses()
			runtimeStatus, err := doctorRuntimeStatus()
			if err != nil {
				return err
			}
			findings = append(findings, runtimeHTTPSFindings(runtimeStatus)...)
			herdrSession := observeCurrentHerdrSession(cmd.Context(), fleetHome)
			tools := doctorManagedTools(runtimeStatus, projects)
			integrations, err := integration.DefaultStore().List()
			if err != nil {
				return err
			}
			blocking := doctorBlockingForRuntime(failing, runtimeStatus.Ready, tools, harnesses)
			next := doctorNext(blocking)

			var doc axi.Doc
			doc.Field("file", path)
			doc.Field("version", info.Version)
			doc.Field("channel", info.Channel)
			doc.Field("commit", selfupdate.DisplayCommit(info.Commit))
			doc.Field("distribution", info.Distribution)
			doc.Int("count", len(findings))
			doc.Int("violations", failing)
			doc.Bool("runtime_ready", runtimeStatus.Ready)
			doc.Field("runtime_target", runtimeStatus.Target)
			doc.Field("runtime_id", valueOrNone(runtimeStatus.RuntimeID))
			doc.Field("runtime_bundle", valueOrNone(runtimeStatus.BundleDir))
			doc.Field("git_version", valueOrNone(runtimeStatus.GitVersion))
			doc.Field("treehouse_version", valueOrNone(runtimeStatus.TreehouseVersion))
			doc.Field("herdr_version", valueOrNone(runtimeStatus.HerdrVersion))
			doc.Field("runtime_reason", valueOrNone(runtimeStatus.Reason))
			appendHerdrSession(&doc, herdrSession)
			axi.Table(&doc, "tools", tools, toolReadinessFields)
			axi.Table(&doc, "supervisor_harnesses", harnesses, harnessReadinessFields)
			doc.Bool("ready", len(blocking) == 0)
			doc.List("blocking", blocking)
			doc.List("next", next)
			axi.Table(&doc, "integrations", integrations, integrationFields)
			appendSupervisionDiagnostics(&doc, cmd.Context(), fleetHome)
			axi.Table(&doc, "findings", findings, cols)
			doc.Help(doctorHelp(len(findings), failing)...)
			if err := doc.Render(cmd.OutOrStdout()); err != nil {
				return err
			}
			if failing > 0 {
				return fmt.Errorf("%s: %d issue(s) found", path, failing)
			}
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&fields, "fields", nil, fieldsFlagUsage(doctorFields, doctorDefaultFields))
	return cmd
}

func doctorRuntimeStatus() (toolchain.Status, error) {
	store, err := toolchain.DefaultStore()
	if err != nil {
		return toolchain.Status{}, err
	}
	status, err := store.Status("", "")
	if err != nil {
		return toolchain.Status{}, err
	}
	if legacyDoctorCompatibility() && !status.Ready && onPath("git") && onPath("treehouse") && onPath("herdr") {
		status.Ready = true
		status.Reason = "test-only legacy tool fixture"
		status.GitHTTPSReady = true
	}
	return status, nil
}

func legacyDoctorCompatibility() bool {
	return legacyDoctorCompat
}

// A missing https helper is a warning, not blocking: the bundle is otherwise intact and ssh
// keeps working, so a fleet home that only clones over ssh must not read as unready with no
// treatment (hand#440). A runtime that fails Ready already has its own "runtime" blocking entry.
func runtimeHTTPSFindings(status toolchain.Status) []doctorFinding {
	if !status.Ready || status.GitHTTPSReady {
		return nil
	}
	return []doctorFinding{{Severity: doctorWarning, Text: "runtime git cannot clone https:// remotes: " + gitHTTPSTreatment("https")}}
}

func doctorFindings(fleetHome string, histories []state.TaskHistory) ([]doctorFinding, error) {
	findings := make([]doctorFinding, 0)

	agentsViolations, err := agentsmd.Check(fleetHome)
	if err != nil {
		return nil, err
	}
	for _, violation := range agentsViolations {
		severity := doctorError
		if violation.Severity == agentsmd.SeverityInfo {
			severity = doctorInfo
		}
		findings = append(findings, doctorFinding{Line: violation.Line, Severity: severity, Text: violation.Text})
	}

	skillViolations, err := skill.Check(fleetHome)
	if err != nil {
		return nil, err
	}
	for _, violation := range skillViolations {
		severity := doctorError
		if violation.Severity == skill.SeverityInfo {
			severity = doctorInfo
		}
		findings = append(findings, doctorFinding{Severity: severity, Text: violation.Text})
	}
	if legacyDoctorCompatibility() {
		for _, tool := range foundationalTools {
			if !onPath(tool) {
				findings = append(findings, doctorFinding{Severity: doctorWarning, Text: fmt.Sprintf("required tool %q is not on PATH", tool)})
			}
		}
	}

	for _, history := range histories {
		task := history.Task
		briefPath := filepath.Join(fleetHome, task.Brief)
		reportPath, err := filepath.Abs(state.ReportPath(fleetHome, task.ID))
		if err != nil {
			findings = append(findings, doctorFinding{Severity: doctorWarning, Text: fmt.Sprintf("task %q report path could not be resolved: %v", task.ID, err)})
			continue
		}
		declares, err := briefDeclaresReportPath(briefPath, reportPath)
		if err != nil {
			findings = append(findings, doctorFinding{Severity: doctorWarning, Text: fmt.Sprintf("task %q brief could not be read: %v", task.ID, err)})
			continue
		}
		if !declares {
			findings = append(findings, doctorFinding{Severity: doctorWarning, Text: fmt.Sprintf("task %q brief does not declare report path", task.ID)})
		}
	}

	for _, history := range histories {
		task := history.Task
		if task.Kind != state.KindShip || task.PR != "" {
			continue
		}
		lines, err := state.ReadReportLines(fleetHome, task.ID)
		if err != nil {
			findings = append(findings, doctorFinding{Severity: doctorWarning, Text: fmt.Sprintf("task %q report could not be read: %v", task.ID, err)})
			continue
		}
		last, ok := state.LastReportedState(lines)
		if !ok || last.State != state.ReportDone {
			continue
		}
		findings = append(findings, doctorFinding{Severity: doctorWarning, Text: fmt.Sprintf("task %q is an open ship task that reported done with no pull request recorded; run `hand status %s` to see what unblocks it", task.ID, task.ID)})
	}

	projects, err := project.ListReadOnly(fleetHome)
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		if issue := gateIssue(fleetHome, p); issue != "" {
			findings = append(findings, doctorFinding{Severity: doctorError, Text: fmt.Sprintf("project %q no-mistakes gate is %s", p.Name, issue)})
		}
	}
	findings = append(findings, doctorWorktreeFindings(fleetHome, histories)...)
	findings = append(findings, doctorLeaseHolderFindings(fleetHome, projects)...)

	detection, err := harness.DetectCurrent()
	if err != nil {
		return nil, err
	}
	snapshot, err := routing.LoadExecutionSnapshot(fleetHome, detection.Name, true)
	if err != nil {
		return nil, err
	}
	if len(snapshot.Config.Profiles) == 0 && len(snapshot.Config.Routes) == 0 && onlyMissingRoutes(snapshot.Config.Problems) {
		severity := doctorWarning
		text := legacyRoutingFinding(snapshot.Legacy, "routing falls back to legacy defaults without explicit intent")
		if snapshot.Legacy.ConfiguredHarness != "" {
			severity = doctorInfo
			text = legacyRoutingFinding(snapshot.Legacy, "routing resolves through explicit legacy defaults")
		}
		return append(findings, doctorFinding{Severity: severity, Text: text}), nil
	}
	for _, problem := range snapshot.Config.Problems {
		findings = append(findings, doctorFinding{Severity: doctorWarning, Text: routingProblemFinding(problem)})
		if problem.Kind != "" && problem.ExecutionClass != "" {
			findings = append(findings, doctorFinding{Severity: doctorWarning, Text: routingDecisionProblem(problem)})
		}
	}
	if len(snapshot.Config.Profiles) == 0 && len(snapshot.Config.Routes) == 0 {
		return append(findings, doctorFinding{Severity: doctorWarning, Text: legacyRoutingFinding(snapshot.Legacy, "routing effective fallback after configuration problems")}), nil
	}
	for _, route := range snapshot.Config.Routes {
		profile, found := profileByName(snapshot.Config.Profiles, route.Profile)
		if found {
			findings = append(findings, doctorFinding{Severity: doctorInfo, Text: fmt.Sprintf("routing decision: %s.%s -> profile %q -> %s", route.Kind, route.ExecutionClass, profile.Name, profileDetails(profile))})
		}
	}
	return findings, nil
}

func briefDeclaresReportPath(briefPath, reportPath string) (bool, error) {
	data, err := os.ReadFile(briefPath)
	if err != nil {
		info, statErr := os.Stat(briefPath)
		if os.IsNotExist(err) || (statErr == nil && info.IsDir()) {
			return false, nil
		}
		return false, err
	}
	text := string(data)
	for start := 0; start < len(text); {
		at := strings.Index(text[start:], reportPath)
		if at < 0 {
			return false, nil
		}
		end := start + at + len(reportPath)
		if end == len(text) || !reportPathSuffix(text, end) {
			return true, nil
		}
		start += at + 1
	}
	return false, nil
}

func reportPathSuffix(text string, end int) bool {
	// A report path is declared only when it is not followed by a path/suffix character, or by a
	// dot followed by one; punctuation, a bare dot, and a newline are valid boundaries.
	next := text[end]
	if next == '.' && end+1 < len(text) && isReportPathSuffixChar(text[end+1]) {
		return true
	}
	return isReportPathSuffixChar(next)
}

func isReportPathSuffixChar(char byte) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '/' || char == '\\'
}

func doctorWorktreeFindings(fleetHome string, histories []state.TaskHistory) []doctorFinding {
	findings := make([]doctorFinding, 0)
	for _, history := range histories {
		attempt := history.ActiveAttempt
		if attempt == nil || attempt.Worktree == "" {
			continue
		}
		clone := filepath.Join(fleetHome, "projects", history.Task.Project)
		expected := filepath.Join(clone, ".git")
		if _, err := os.Stat(expected); err != nil {
			findings = append(findings, doctorFinding{Severity: doctorError, Text: fmt.Sprintf("task %q registered clone cannot be inspected: %v", history.Task.ID, err)})
			continue
		}
		actual, err := git.CommonDir(attempt.Worktree)
		if err != nil {
			findings = append(findings, doctorFinding{Severity: doctorError, Text: fmt.Sprintf("task %q worktree common directory cannot be inspected: %v", history.Task.ID, err)})
			continue
		}
		if git.SamePath(expected, actual) {
			continue
		}
		findings = append(findings, doctorFinding{Severity: doctorError, Text: fmt.Sprintf("task %q worktree is rooted in another Git repository: got %s, want %s", history.Task.ID, actual, expected)})
	}
	return findings
}

// A leased slot's holder classifies into exactly one of four cases - absent from the Fleet
// registry, registered but not ready, unparseable, or registered and ready (atqamz/hand#432) -
// and only the first three are reported. Nothing here reclaims a lease.
func doctorLeaseHolderFindings(fleetHome string, projects []project.Project) []doctorFinding {
	type leasedHolder struct {
		path, fleetID, taskID string
	}
	findings := make([]doctorFinding, 0)
	var toClassify []leasedHolder

	for _, p := range projects {
		clone := filepath.Join(fleetHome, "projects", p.Name)
		if _, err := os.Stat(clone); err != nil {
			continue
		}
		entries, err := worktree.PoolStatus(clone)
		if err != nil {
			findings = append(findings, doctorFinding{Severity: doctorWarning, Text: fmt.Sprintf("project %q worktree pool could not be read: %v", p.Name, err)})
			continue
		}
		for _, entry := range entries {
			if entry.Status != "leased" || entry.LeaseHolder == "" {
				continue
			}
			fleetID, taskID, ok := worktree.ParseLeaseHolder(entry.LeaseHolder)
			if !ok {
				findings = append(findings, doctorFinding{Severity: doctorWarning, Text: fmt.Sprintf("pool slot %s is leased to %q, which does not parse as a Hand lease holder (want hand:<fleet-id>:<task-id>); it is not reclaimed automatically, inspect it with `treehouse status` before touching the slot", entry.Path, entry.LeaseHolder)})
				continue
			}
			toClassify = append(toClassify, leasedHolder{path: entry.Path, fleetID: fleetID, taskID: taskID})
		}
	}
	// Nothing here needs the registry looked up at all, so a registry that cannot be consulted is
	// not itself a finding: there is no holder waiting on the answer.
	if len(toClassify) == 0 {
		return findings
	}

	registryPath, err := registry.Path()
	if err != nil {
		return append(findings, doctorFinding{Severity: doctorWarning, Text: fmt.Sprintf("Fleet registry could not be resolved: %v; pool lease holders were not checked", err)})
	}
	var fleets []registry.Fleet
	registryDB, err := registry.OpenReadOnlyAt(registryPath)
	switch {
	case errors.Is(err, registry.ErrRegistryMissing):
		return append(findings, doctorFinding{Severity: doctorWarning, Text: fmt.Sprintf("Fleet registry %s does not exist; no pool lease holder could be checked against it, run `hand init` to re-register this fleet home", registryPath)})
	case err != nil:
		return append(findings, doctorFinding{Severity: doctorWarning, Text: fmt.Sprintf("Fleet registry %s could not be read: %v; pool lease holders were not checked", registryPath, err)})
	default:
		defer func() { _ = registryDB.Close() }()
		fleets, err = registryDB.List(fleetHome)
		if err != nil {
			return append(findings, doctorFinding{Severity: doctorWarning, Text: fmt.Sprintf("Fleet registry %s could not be read: %v; pool lease holders were not checked", registryPath, err)})
		}
	}
	states := make(map[string]registry.State, len(fleets))
	for _, fleet := range fleets {
		states[fleet.ID] = fleet.State
	}

	for _, holder := range toClassify {
		switch state, known := states[holder.fleetID]; {
		case !known:
			findings = append(findings, doctorFinding{Severity: doctorError, Text: fmt.Sprintf("pool slot %s is leased to task %q of Fleet %s, which is absent from the Fleet registry; the lease is not reclaimed automatically, run `hand fleet` and decide with the operator", holder.path, holder.taskID, holder.fleetID)})
		case state != registry.StateReady:
			findings = append(findings, doctorFinding{Severity: doctorWarning, Text: fmt.Sprintf("pool slot %s is leased to task %q of Fleet %s, which is registered but not ready (%s); the lease is not reclaimed automatically, run `hand fleet` to see why", holder.path, holder.taskID, holder.fleetID, state)})
		}
	}
	return findings
}

// A local-only fleet never needs gh, while a registered project delivering through direct-pr or
// no-mistakes does.
func ghRequired(projects []project.Project) bool {
	for _, p := range projects {
		if p.Mode == project.ModeDirectPR || p.Mode == project.ModeNoMistakes {
			return true
		}
	}
	return false
}

func doctorTools(projects []project.Project) []toolReadiness {
	tools := make([]toolReadiness, 0, len(foundationalTools)+1)
	for _, tool := range foundationalTools {
		tools = append(tools, toolReadiness{Tool: tool, Installed: onPath(tool), Required: true})
	}
	tools = append(tools, toolReadiness{Tool: "gh", Installed: optionalInstalled("github/gh"), Required: ghRequired(projects)})
	return tools
}

func doctorManagedTools(status toolchain.Status, projects []project.Project) []toolReadiness {
	if legacyDoctorCompatibility() {
		return doctorTools(projects)
	}
	return []toolReadiness{
		{Tool: "git", Installed: status.Ready, Required: true},
		{Tool: "treehouse", Installed: status.Ready, Required: true},
		{Tool: "herdr", Installed: status.Ready, Required: true},
		{Tool: "gh", Installed: optionalInstalled("github/gh"), Required: ghRequired(projects)},
	}
}

func optionalInstalled(id string) bool {
	status, err := integration.DefaultStore().List()
	if err != nil {
		return false
	}
	for _, item := range status {
		if item.Capability.ID == id {
			return item.State == integration.StateInstalled
		}
	}
	return false
}

func doctorBlockingForRuntime(failing int, runtimeReady bool, tools []toolReadiness, harnesses []harnessReadiness) []string {
	if legacyDoctorCompatibility() {
		return doctorBlocking(failing, tools, harnesses)
	}
	blocking := make([]string, 0)
	if failing > 0 {
		blocking = append(blocking, "fleet-health")
	}
	if !runtimeReady {
		blocking = append(blocking, "runtime")
	}
	for _, tool := range tools {
		if tool.Tool != "git" && tool.Tool != "treehouse" && tool.Tool != "herdr" && tool.Required && !tool.Installed {
			blocking = append(blocking, tool.Tool)
		}
	}
	if !anyHarnessInstalled(harnesses) {
		blocking = append(blocking, "harness")
	}
	return blocking
}

// Every registered Supervisor Harness is reported, never a preferred one: bootstrap and doctor
// both only detect, they do not choose. Worker-only providers must not satisfy Supervisor readiness.
func doctorHarnesses() []harnessReadiness {
	names := supervision.SupervisorHosts()
	out := make([]harnessReadiness, 0, len(names))
	for _, name := range names {
		out = append(out, harnessReadiness{Name: name, Installed: onPath(harness.Executable(name))})
	}
	return out
}

func anyHarnessInstalled(harnesses []harnessReadiness) bool {
	for _, h := range harnesses {
		if h.Installed {
			return true
		}
	}
	return false
}

// The one list a bootstrapper needs to decide readiness without re-deriving the rules above: a
// fleet-health entry stands in for the error findings already detailed in `findings`, a missing
// required tool names itself, and a fleet with no installed harness cannot be driven.
func doctorBlocking(failing int, tools []toolReadiness, harnesses []harnessReadiness) []string {
	blocking := make([]string, 0)
	if failing > 0 {
		blocking = append(blocking, "fleet-health")
	}
	for _, tool := range tools {
		if tool.Required && !tool.Installed {
			blocking = append(blocking, tool.Tool)
		}
	}
	if !anyHarnessInstalled(harnesses) {
		blocking = append(blocking, "harness")
	}
	return blocking
}

// One exact recovery action per blocking entry, in the same order, so a caller never has to
// reconcile two lists that could drift apart.
func doctorNext(blocking []string) []string {
	next := make([]string, 0, len(blocking))
	for _, item := range blocking {
		switch item {
		case "fleet-health":
			next = append(next, "resolve every error finding reported above")
		case "harness":
			next = append(next, "install and authenticate at least one supported Supervisor Harness (see `supervisor_harnesses` above), then run hand doctor")
		case "runtime":
			next = append(next, "run `hand runtime ensure`")
		default:
			next = append(next, fmt.Sprintf("install %s", item))
		}
	}
	return next
}

func onlyMissingRoutes(problems []routing.ConfigProblem) bool {
	for _, problem := range problems {
		if problem.Code != routing.ConfigProblemMissingRoute {
			return false
		}
	}
	return true
}

func legacyRoutingFinding(defaults routing.LegacyDefaults, prefix string) string {
	details := []string{fmt.Sprintf("harness %q", defaults.Harness)}
	if model := defaults.Models[defaults.Harness]; model != "" {
		details = append(details, fmt.Sprintf("model %q", model))
	}
	if effort := defaults.Efforts[defaults.Harness]; effort != "" {
		details = append(details, fmt.Sprintf("effort %q", effort))
	}
	return prefix + ": " + strings.Join(details, ", ")
}

func profileByName(profiles []routing.Profile, name string) (routing.Profile, bool) {
	for _, profile := range profiles {
		if profile.Name == name {
			return profile, true
		}
	}
	return routing.Profile{}, false
}

func profileDetails(profile routing.Profile) string {
	details := []string{fmt.Sprintf("harness %q", profile.Harness)}
	if profile.Model != "" {
		details = append(details, fmt.Sprintf("model %q", profile.Model))
	}
	if profile.Effort != "" {
		details = append(details, fmt.Sprintf("effort %q", profile.Effort))
	}
	return strings.Join(details, ", ")
}

func routingDecisionProblem(problem routing.ConfigProblem) string {
	details := strings.TrimPrefix(problem.Message, "route ")
	return fmt.Sprintf("routing decision: %s.%s -> unavailable (%s)", problem.Kind, problem.ExecutionClass, details)
}

func routingProblemFinding(problem routing.ConfigProblem) string {
	if problem.Kind != "" && problem.ExecutionClass != "" {
		return fmt.Sprintf("routing drift: route %s.%s %s", problem.Kind, problem.ExecutionClass, strings.TrimPrefix(problem.Message, "route "))
	}
	if problem.Profile != "" {
		return fmt.Sprintf("routing drift: profile %q %s", problem.Profile, problem.Message)
	}
	return "routing drift: " + problem.Message
}

// hand doctor fixes nothing, so what it owes a reader is which findings are
// theirs to edit and which one command repairs on its own.
func doctorHelp(count, failing int) []string {
	if count == 0 {
		return nil
	}
	if failing == 0 {
		return []string{"No error findings, so this run passed; inspect warnings and info before the next dispatch"}
	}
	return []string{
		"Resolve every error finding; hand doctor reports and never rewrites",
		"Run `hand init` to restore AGENTS.md or the bundled skill; a foreign file conflict at a skill destination must be moved aside by hand first",
		"Inspect project gates and routing configuration for the remaining fleet health findings",
	}
}
