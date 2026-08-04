package cmd

import (
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/atqamz/secondhand/internal/agentsmd"
	"github.com/atqamz/secondhand/internal/axi"
	"github.com/atqamz/secondhand/internal/home"
	"github.com/spf13/cobra"
)

// A whole-file finding has no line to anchor to, and a 0 there would read as
// one rather than as its absence.
var doctorFields = []axi.Column[agentsmd.Violation]{
	{Name: "line", Value: func(v agentsmd.Violation) string {
		if v.Line == 0 {
			return "none"
		}
		return strconv.Itoa(v.Line)
	}},
	{Name: "severity", Value: func(v agentsmd.Violation) string {
		if v.Severity == agentsmd.SeverityInfo {
			return "info"
		}
		return "violation"
	}},
	{Name: "finding", Value: func(v agentsmd.Violation) string { return v.Text }},
}

var doctorDefaultFields = []string{"line", "severity", "finding"}

func newDoctorCmd() *cobra.Command {
	var fields []string

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the fleet home's AGENTS.md for perishable content and generated-block drift",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			violations, err := agentsmd.Check(fleetHome)
			if err != nil {
				return err
			}
			cols, err := pickFields(doctorFields, fields, doctorDefaultFields)
			if err != nil {
				return err
			}

			path := filepath.Join(fleetHome, "AGENTS.md")
			failing := 0
			for _, v := range violations {
				if v.Severity != agentsmd.SeverityInfo {
					failing++
				}
			}

			var doc axi.Doc
			doc.Field("file", path)
			doc.Int("count", len(violations))
			doc.Int("violations", failing)
			axi.Table(&doc, "findings", violations, cols)
			doc.Help(doctorHelp(len(violations), failing)...)
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

// hand doctor fixes nothing, so what it owes a reader is which findings are
// theirs to edit and which one command repairs on its own.
func doctorHelp(count, failing int) []string {
	if count == 0 {
		return nil
	}
	if failing == 0 {
		return []string{"Every finding here is informational, so this run passed; nothing has to change"}
	}
	return []string{
		"Edit AGENTS.md to resolve each finding; hand doctor reports and never rewrites",
		"Run `hand update` if the finding is generated-block drift, since that block is refreshed rather than hand-edited",
	}
}
