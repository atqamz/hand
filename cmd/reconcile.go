package cmd

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/runtime"
	"github.com/spf13/cobra"
)

func newReconcileCmd() *cobra.Command {
	var asJSON bool
	var abandonWorktree bool
	cmd := &cobra.Command{
		Use:   "reconcile [id]",
		Short: "Converge durable task state with observed external reality",
		Long: "Converge durable task state with observed external reality.\n\n" +
			"--abandon-worktree attests that Hand relinquishes a recorded Treehouse lease whose pool cannot be\n" +
			"observed at all, for instance after the pool key moved. It needs an explicit task ID, it refuses any\n" +
			"lease an observation can still prove or disprove, and it never returns, prunes or deletes a worktree:\n" +
			"the worktree is left exactly as it is for the operator to reclaim through treehouse itself. The flag\n" +
			"only adds this worktree attestation and leaves every other reconciliation action unchanged, including\n" +
			"ordinary Herdr cleanup, which still requires proven ownership. An attempt that is still active is\n" +
			"reached only through a recorded teardown decision, the shape a teardown interrupted at the worktree\n" +
			"step leaves behind, while an attempt whose lifecycle is already terminal is eligible on its own once\n" +
			"its worktree resource is unsettled. No worktree of a live worker can be abandoned on either route,\n" +
			"because a provisioning or running attempt is skipped and the active attempt is reachable only through\n" +
			"that recorded decision.",
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			if abandonWorktree && id == "" {
				return asPrecondition(runtime.Precondition(errors.New("--abandon-worktree needs an explicit task ID")))
			}
			report, reconcileErr := runtime.New().Reconcile(runtime.ReconcileRequest{
				Context: cmd.Context(), Home: fleetHome, ID: id, AbandonWorktree: abandonWorktree,
			})
			if err := renderReconcileReport(cmd, report, asJSON); err != nil {
				return err
			}
			return reconcileReportError(report, reconcileErr)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON instead of TOON")
	cmd.Flags().BoolVar(&abandonWorktree, "abandon-worktree", false, "attest that Hand relinquishes an unobservable Treehouse lease without touching the worktree")
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
		if result.Detail != "" {
			doc.Field("detail", result.Detail)
		}
		if result.Error != "" {
			doc.Field("error", result.Error)
		}
	}
	if len(report.Anomalies) > 0 {
		anomalies := make([]string, 0, len(report.Anomalies))
		for _, anomaly := range report.Anomalies {
			owner := "unattributed"
			if anomaly.OwnerAttemptID != 0 {
				owner = fmt.Sprintf("%d", anomaly.OwnerAttemptID)
			}
			anomalies = append(anomalies, fmt.Sprintf("%s:%s/%s owner_attempt=%s reason=%s", anomaly.Kind, anomaly.WorkspaceID, anomaly.TabID, owner, anomaly.Reason))
		}
		doc.List("anomalies", anomalies)
	}
	if len(report.Errors) > 0 {
		doc.List("errors", report.Errors)
	}
	return doc.Render(cmd.OutOrStdout())
}

func reconcileReportError(report runtime.ReconcileReport, reconcileErr error) error {
	var errs []error
	if repair := firstRepair(report); repair != nil {
		errs = append(errs, runtime.Precondition(fmt.Errorf("task %q needs repair: %s", repair.ID, repair.RepairCode)))
	}
	if anomaly := firstAnomaly(report); anomaly != nil {
		detail := fmt.Sprintf("Herdr anomaly %q at %s/%s", anomaly.Kind, anomaly.WorkspaceID, anomaly.TabID)
		if anomaly.OwnerAttemptID != 0 {
			detail += fmt.Sprintf(" (attempt %d)", anomaly.OwnerAttemptID)
		}
		errs = append(errs, runtime.Precondition(errors.New(detail)))
	}
	if reconcileErr != nil {
		errs = append(errs, reconcileErr)
	}
	return asPrecondition(errors.Join(errs...))
}

func firstRepair(report runtime.ReconcileReport) *runtime.ReconcileResult {
	for i := range report.Results {
		if report.Results[i].Outcome == "needs-repair" {
			return &report.Results[i]
		}
	}
	return nil
}

func firstAnomaly(report runtime.ReconcileReport) *runtime.ReconcileAnomaly {
	if len(report.Anomalies) == 0 {
		return nil
	}
	return &report.Anomalies[0]
}
