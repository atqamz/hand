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
	"github.com/spf13/cobra"
)

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

func newDoctorCmd() *cobra.Command {
	var fields []string

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check fleet health, including AGENTS.md, project gates, and routing",
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
	violations, err := agentsmd.Check(fleetHome)
	if err != nil {
		return nil, err
	}
	findings := make([]doctorFinding, 0, len(violations))
	for _, violation := range violations {
		severity := doctorError
		if violation.Severity == agentsmd.SeverityInfo {
			severity = doctorInfo
		}
		findings = append(findings, doctorFinding{Line: violation.Line, Severity: severity, Text: violation.Text})
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
	if len(snapshot.Config.Profiles) == 0 && len(snapshot.Config.Routes) == 0 {
		severity := doctorWarning
		text := "routing falls back to legacy defaults without explicit intent"
		if snapshot.Legacy.ConfiguredHarness != "" {
			severity = doctorInfo
			text = "routing resolves through explicit legacy defaults"
		}
		return append(findings, doctorFinding{Severity: severity, Text: text}), nil
	}
	for _, problem := range snapshot.Config.Problems {
		findings = append(findings, doctorFinding{Severity: doctorWarning, Text: routingProblemFinding(problem)})
	}
	return append(findings, doctorFinding{Severity: doctorInfo, Text: "routing resolves through configured profiles"}), nil
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
		"Run `hand update` for generated-block drift, or inspect project gates and routing configuration for fleet health findings",
	}
}
