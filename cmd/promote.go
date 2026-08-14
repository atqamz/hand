package cmd

import (
	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/runtime"
	"github.com/spf13/cobra"
)

func newPromoteCmd() *cobra.Command {
	var profile string
	var harnessName string
	var model string
	var effort string
	var skipGateCheck bool

	cmd := &cobra.Command{
		Use:   "promote <id>",
		Short: "Promote a completed scout task into a ship task",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			result, err := runtime.New().Promote(cmd.Context(), runtime.PromoteRequest{
				Home: fleetHome, ID: args[0], Profile: profile, ProfileFromFlag: cmd.Flags().Changed("profile"),
				Harness: harnessName, HarnessFromFlag: cmd.Flags().Changed("harness"),
				Model: model, ModelFromFlag: cmd.Flags().Changed("model"),
				Effort: effort, EffortFromFlag: cmd.Flags().Changed("effort"), SkipGateCheck: skipGateCheck,
			})
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
			doc.Field("result", "promoted")
			doc.Field("kind", result.Kind)
			doc.Field("was", result.Was)
			doc.Field("project", result.Project)
			doc.Field("harness", result.Harness)
			doc.Field("worktree", result.Worktree)
			doc.Help(result.Help...)
			return doc.Render(cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "", "execution profile override")
	cmd.Flags().StringVar(&harnessName, "harness", "", "harness for the new ship worker (default: config/harness, then the detected supervisor harness)")
	cmd.Flags().StringVar(&model, "model", "", "model override for harnesses that support it")
	cmd.Flags().StringVar(&effort, "effort", "", "effort override for harnesses that support it")
	cmd.Flags().BoolVar(&skipGateCheck, "skip-gate-check", false, "dispatch even if the no-mistakes gate is not initialized, the clone path is missing from disk, or that path is not a git repository")
	return cmd
}
