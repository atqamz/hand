package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/store"
	"github.com/spf13/cobra"
)

func newCutoverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cutover",
		Short: "Inspect or recover an interrupted canonical cutover",
		// Recovery must work with a frozen or absent active DB and must not run
		// legacy startup migration, config reconciliation, or provider provisioning.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "inspect <home>",
		Short: "Classify cutover evidence without changing the Fleet",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCutover(cmd, args[0], false)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "recover <home>",
		Short: "Resume an already-frozen cutover from exact preserved evidence",
		Long: "Resume an interrupted cutover using its exact original archive and recovery evidence.\n" +
			"Rebuild and publish only when the existing recovery classifier proves it safe.\n" +
			"Fresh legacy state is left unchanged; this command does not begin a new cutover.\n" +
			"Registry discovery is a separate projection and is not repaired here.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv(harness.RoleEnv) == harness.WorkerRole {
				return &ExitError{Err: fmt.Errorf("cutover recovery is unavailable when %s=%s", harness.RoleEnv, harness.WorkerRole), Code: 3}
			}
			return runCutover(cmd, args[0], true)
		},
	})
	return cmd
}

func runCutover(cmd *cobra.Command, target string, recoverState bool) error {
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
	var result store.CanonicalV19CutoverRecovery
	if recoverState {
		result, err = store.RecoverCanonicalV19Cutover(homeDir)
	} else {
		result, err = store.InspectCanonicalV19Cutover(homeDir)
	}
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
