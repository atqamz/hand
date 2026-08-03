package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/atqamz/secondhand/internal/agentsmd"
	"github.com/atqamz/secondhand/internal/home"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
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

			path := filepath.Join(fleetHome, "AGENTS.md")
			out := cmd.OutOrStdout()
			failing := 0
			for _, v := range violations {
				if v.Severity != agentsmd.SeverityInfo {
					failing++
				}
				if v.Line > 0 {
					if _, err := fmt.Fprintf(out, "%s:%d: %s\n", path, v.Line, v.Text); err != nil {
						return err
					}
					continue
				}
				if _, err := fmt.Fprintf(out, "%s: %s\n", path, v.Text); err != nil {
					return err
				}
			}
			if failing > 0 {
				return fmt.Errorf("%s: %d issue(s) found", path, failing)
			}
			return nil
		},
	}
	return cmd
}
