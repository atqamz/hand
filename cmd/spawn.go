package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/runtime"
	"github.com/atqamz/hand/internal/state"
	"github.com/spf13/cobra"
)

func newSpawnCmd() *cobra.Command {
	var scout bool
	var harnessName string
	var model string
	var effort string
	var skipGateCheck bool

	cmd := &cobra.Command{
		Use:   "spawn <id> <project>",
		Short: "Spawn a worker agent in an isolated worktree",
		Args:  usageArgs(cobra.ExactArgs(2)),
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

			kind := state.KindShip
			if scout {
				kind = state.KindScout
			}
			result, err := runtime.New().Spawn(cmd.Context(), runtime.SpawnRequest{
				Home: fleetHome, ID: args[0], Project: args[1], Kind: kind,
				Harness: harnessName, HarnessFromFlag: harnessFromFlag, Model: model, Effort: effort,
				SkipGateCheck: skipGateCheck,
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
			doc.Field("result", "spawned")
			doc.Field("project", result.Project)
			doc.Field("kind", result.Kind)
			doc.Field("harness", result.Harness)
			doc.Field("worktree", result.Worktree)
			doc.Help(result.Help...)
			return doc.Render(cmd.OutOrStdout())
		},
	}

	cmd.Flags().BoolVar(&scout, "scout", false, "mark as scout task (deliverable is a report, not a PR)")
	cmd.Flags().StringVar(&harnessName, "harness", "", "agent harness to launch (default: config/harness, then the detected supervisor harness)")
	cmd.Flags().StringVar(&model, "model", "", "model override for harnesses that support it")
	cmd.Flags().StringVar(&effort, "effort", "", "effort level for harnesses that support it")
	cmd.Flags().BoolVar(&skipGateCheck, "skip-gate-check", false, "dispatch even if the no-mistakes gate is not initialized, the clone path is missing from disk, or that path is not a git repository")
	return cmd
}

func configDefault(home, name, fallback string) string {
	data, err := os.ReadFile(filepath.Join(home, "config", name))
	if err != nil {
		return fallback
	}
	return strings.TrimSpace(string(data))
}

func configSeconds(home, name string, fallback time.Duration) (time.Duration, error) {
	raw := configDefault(home, name, "")
	if raw == "" {
		return fallback, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid config/%s %q: %w", name, raw, err)
	}
	return time.Duration(seconds) * time.Second, nil
}
