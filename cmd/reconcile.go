package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/runtime"
	"github.com/spf13/cobra"
)

func newReconcileCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "reconcile [id]",
		Short: "Converge durable task state with observed external reality",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			report, reconcileErr := runtime.New().Reconcile(runtime.ReconcileRequest{Context: cmd.Context(), Home: fleetHome, ID: id})
			if err := renderReconcileReport(cmd, report, asJSON); err != nil {
				return err
			}
			if repair := firstRepair(report); repair != nil {
				return asPrecondition(runtime.Precondition(fmt.Errorf("task %q needs repair: %s", repair.ID, repair.RepairCode)))
			}
			if reconcileErr != nil {
				return asPrecondition(reconcileErr)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON instead of TOON")
	return cmd
}

func renderReconcileReport(cmd *cobra.Command, report runtime.ReconcileReport, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	var doc axi.Doc
	doc.Int("count", len(report.Results))
	for _, result := range report.Results {
		doc.Field("id", result.ID)
		doc.Field("result", result.Outcome)
		doc.Field("action", orNone(result.Action))
		doc.Int("iterations", result.Iterations)
		if result.AttemptID != 0 {
			doc.Field("attempt", fmt.Sprintf("%d", result.AttemptID))
		}
		if result.RepairCode != "" {
			doc.Field("repair_code", result.RepairCode)
			doc.Field("repair_reason", result.RepairReason)
		}
	}
	return doc.Render(cmd.OutOrStdout())
}

func firstRepair(report runtime.ReconcileReport) *runtime.ReconcileResult {
	for i := range report.Results {
		if report.Results[i].Outcome == "needs-repair" {
			return &report.Results[i]
		}
	}
	return nil
}
