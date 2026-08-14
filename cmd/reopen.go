package cmd

import (
	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/runtime"
	"github.com/spf13/cobra"
)

func newReopenCmd() *cobra.Command {
	var harnessName, model, effort string
	var skipGateCheck bool

	cmd := &cobra.Command{
		Use:   "reopen <id>",
		Short: "Start a new attempt for a terminal task",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			harnessFromFlag := harnessName != ""
			if !harnessFromFlag {
				cfg, err := currentWorkerConfig(fleetHome)
				if err != nil {
					return err
				}
				harnessName = cfg.harness
			}

			result, err := runtime.New().Reopen(cmd.Context(), runtime.ReopenRequest{
				Home: fleetHome, ID: args[0], Harness: harnessName, HarnessFromFlag: harnessFromFlag,
				Model: model, Effort: effort, SkipGateCheck: skipGateCheck,
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
			doc.Field("result", "reopened")
			doc.Field("attempt", result.Attempt)
			doc.Field("project", result.Project)
			doc.Field("kind", result.Kind)
			doc.Field("harness", result.Harness)
			doc.Field("worktree", result.Worktree)
			doc.Help(result.Help...)
			return doc.Render(cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&harnessName, "harness", "", "agent harness to launch (default: config/harness, then the detected supervisor harness)")
	cmd.Flags().StringVar(&model, "model", "", "model override for harnesses that support it")
	cmd.Flags().StringVar(&effort, "effort", "", "effort level for harnesses that support it")
	cmd.Flags().BoolVar(&skipGateCheck, "skip-gate-check", false, "dispatch even if the no-mistakes gate is not initialized, the clone path is missing from disk, or that path is not a git repository")
	return cmd
}
