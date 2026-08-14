package cmd

import (
	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/runtime"
	"github.com/spf13/cobra"
)

func newTeardownCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "teardown <id>",
		Short: "Clean up a completed task",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			result, err := runtime.New().Teardown(cmd.Context(), runtime.TeardownRequest{Home: fleetHome, ID: args[0], Force: force})
			if err != nil {
				if warningErr := renderRuntimeWarnings(cmd, runtime.Warnings(err)); warningErr != nil {
					return warningErr
				}
				return asPrecondition(err)
			}
			if err := renderRuntimeWarnings(cmd, result.Warnings); err != nil {
				return err
			}

			var doc axi.Doc
			doc.Field("id", result.ID)
			doc.Field("result", "torn-down")
			doc.Field("project", result.Project)
			doc.Field("kind", result.Kind)
			doc.Field("outcome", result.Outcome)
			doc.Field("detail", orNone(result.Detail))
			doc.Field("worktree", result.Worktree)
			doc.Help(result.Help...)
			return doc.Render(cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "skip landed-work checks")
	return cmd
}
