package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/runtime"
	"github.com/atqamz/hand/internal/store"
	"github.com/spf13/cobra"
)

func newCutoverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cutover",
		Short: "Inspect, run offline, or recover a canonical cutover",
		// Recovery must work with a frozen or absent active DB and must not run
		// legacy startup migration, config reconciliation, or provider provisioning.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "inspect <home>",
		Short: "Classify cutover evidence without changing the Fleet",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCutover(cmd, args[0], store.InspectCanonicalV19Cutover)
		},
	})
	offline := &cobra.Command{
		Use:   "offline <home>",
		Short: "Freeze a legacy home, then complete its cutover after a full restart",
		Long: "The first run freezes the exact v0.7.2 legacy DB; legacy access stops until completion or abort.\n" +
			"After a full restart of the machine, the next run proves the restart, re-checks every frozen\n" +
			"Project, Treehouse, and Herdr fact, and publishes canonical v19. Drift refuses; --abort then\n" +
			"restores the exact pre-freeze legacy DB. A local filesystem shared with another kernel (an export,\n" +
			"a VM or container share) is not covered, and a container or image without a persistent\n" +
			"/etc/machine-id cannot run the offline cutover. Live cutover of a running home is always refused.",
		Args: usageArgs(cobra.ExactArgs(1)),
	}
	abort := offline.Flags().Bool("abort", false, "return a frozen, unpublished home to its exact pre-freeze legacy DB")
	offline.RunE = func(cmd *cobra.Command, args []string) error {
		if os.Getenv(harness.RoleEnv) == harness.WorkerRole {
			return &ExitError{Err: fmt.Errorf("offline cutover is unavailable when %s=%s", harness.RoleEnv, harness.WorkerRole), Code: 3}
		}
		if *abort {
			return runCutover(cmd, args[0], store.AbortCanonicalV19Cutover)
		}
		return runCutover(cmd, args[0], func(home string) (store.CanonicalV19CutoverRecovery, error) {
			return runtime.OfflineCutover(cmd.Context(), home)
		})
	}
	cmd.AddCommand(offline)
	cmd.AddCommand(&cobra.Command{
		Use:   "recover <home>",
		Short: "Resume an already-frozen cutover from exact preserved evidence",
		Long: "Resume an interrupted cutover using its exact original archive and recovery evidence.\n" +
			"Rebuild and publish only after a restart since the freeze and a clean drift gate.\n" +
			"Fresh legacy state is left unchanged; this command does not begin a new cutover.\n" +
			"Registry discovery is a separate projection and is not repaired here.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv(harness.RoleEnv) == harness.WorkerRole {
				return &ExitError{Err: fmt.Errorf("cutover recovery is unavailable when %s=%s", harness.RoleEnv, harness.WorkerRole), Code: 3}
			}
			return runCutover(cmd, args[0], func(home string) (store.CanonicalV19CutoverRecovery, error) {
				return store.RecoverCanonicalV19Cutover(home, runtime.LegacyV18CutoverDriftGate)
			})
		},
	})
	return cmd
}

func runCutover(cmd *cobra.Command, target string, run func(string) (store.CanonicalV19CutoverRecovery, error)) error {
	homeDir, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve cutover home: %w", err)
	}
	info, err := os.Stat(homeDir)
	if err != nil {
		return fmt.Errorf("inspect cutover home: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("cutover home %s is not a directory", homeDir)
	}
	result, err := run(homeDir)
	if err != nil {
		return err
	}
	var doc axi.Doc
	doc.Field("home", homeDir)
	doc.Field("disposition", result.Disposition)
	doc.Field("fleet_id", valueOrNone(result.FleetID))
	doc.Field("migration_id", valueOrNone(result.MigrationID))
	doc.Field("reason", valueOrNone(result.Reason))
	return doc.Render(cmd.OutOrStdout())
}
