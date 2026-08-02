package cmd

import (
	"fmt"

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

			out := cmd.OutOrStdout()
			for _, v := range violations {
				if v.Line > 0 {
					if _, err := fmt.Fprintf(out, "AGENTS.md:%d: %s\n", v.Line, v.Text); err != nil {
						return err
					}
					continue
				}
				if _, err := fmt.Fprintf(out, "AGENTS.md: %s\n", v.Text); err != nil {
					return err
				}
			}
			if len(violations) > 0 {
				return fmt.Errorf("AGENTS.md: %d issue(s) found", len(violations))
			}
			return nil
		},
	}
	return cmd
}
