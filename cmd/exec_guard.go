package cmd

import (
	"os"

	"github.com/atqamz/hand/internal/execguard"
	"github.com/spf13/cobra"
)

func newExecGuardCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "exec-guard <handoff>",
		Short:  "Run one Launch handoff's harness under a Hand-owned execution guard",
		Hidden: true,
		Args:   usageArgs(cobra.ExactArgs(1)),
		// The guard runs inside a Herdr pane with no fleet-home context and must print nothing
		// of its own there, so the root startup guards and notices do not apply.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, args []string) error {
			code, err := execguard.Run(args[0])
			if err != nil {
				return err
			}
			if code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}
}
