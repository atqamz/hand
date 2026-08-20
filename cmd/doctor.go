package cmd

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/agentsmd"
	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/routing"
	"github.com/atqamz/hand/internal/selfupdate"
	"github.com/atqamz/hand/internal/skill"
	"github.com/spf13/cobra"
)

// The external tools every ordinary dispatch and delivery path needs regardless of which
// projects are registered; no-mistakes is checked separately, per project, since it is only
// required for a project explicitly configured in that delivery mode.
var requiredTools = []string{"treehouse", "herdr", "gh"}

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
			findings, err := doctorFindings(fleetHome)
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

			var doc axi.Doc
			doc.Field("file", path)
			doc.Field("version", info.Version)
			doc.Field("channel", info.Channel)
			doc.Field("commit", selfupdate.DisplayCommit(info.Commit))
			doc.Field("distribution", info.Distribution)
			doc.Int("count", len(findings))
			doc.Int("violations", failing)
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

func doctorFindings(fleetHome string) ([]doctorFinding, error) {
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

	for _, tool := range requiredTools {
		if !onPath(tool) {
			findings = append(findings, doctorFinding{Severity: doctorWarning, Text: fmt.Sprintf("required tool %q is not on PATH", tool)})
		}
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
