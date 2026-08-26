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
	var abandonPane bool
	var attemptNeverStarted bool
	cmd := &cobra.Command{
		Use:   "reconcile [id]",
		Short: "Converge durable task state with observed external reality",
		Long: "Converge durable task state with observed external reality.\n\n" +
			"A running Attempt whose recorded Herdr pane is observed absent reaches a terminal lifecycle here,\n" +
			"without `hand teardown`. Landing evidence picks which one: an observed merge, a recorded delivery\n" +
			"or a scout report on disk completes it, and positively observed unlanded work interrupts it. Where\n" +
			"the landing cannot be observed at all the reported landing is `unknown`, no lifecycle value is\n" +
			"invented, and the condition is recorded as needs-repair instead. Convergence releases no resource\n" +
			"of its own, so a Herdr or worktree resource that needs repair never holds the lifecycle back.\n\n" +
			"A running Attempt whose Herdr pane is still present is also read for `agent_status`: `working` or\n" +
			"`blocked` are reported as such, and `idle-unreported` names a pane that stopped being busy with\n" +
			"nothing on the report channel explaining it since launch. The fact is recorded in the Attempt's\n" +
			"durable status columns, so it survives whether or not `hand watch` was ever running. A pane observed\n" +
			"as Herdr `done` with no report or only a `working:` report also gets terminal convergence after\n" +
			"landing evidence is checked; a `paused:`, `blocked:`, `needs-decision:`, `done:`, or `failed:`\n" +
			"report still explains the stop and leaves the lifecycle for the operator's normal next step.\n" +
			"Where the harness carries a catalogued usage-limit signature, an idle-unreported Attempt is also\n" +
			"checked for it; a detected limit's stated reset time is preserved the same way `hand watch` would\n" +
			"preserve it.\n\n" +
			"Automatic worktree return also requires proof that no commit exists only there: a commit reachable\n" +
			"from a remote-tracking ref, or one GitHub records as the head of the task pull request, is held\n" +
			"elsewhere too. Unpushed commits withhold the return, and so does a comparison that could not be\n" +
			"made at all, recorded as its own condition rather than as work found at risk. The Attempt stays\n" +
			"terminal either way, and reconciling the withheld state again changes nothing.\n\n" +
			"--abandon-worktree attests that Hand relinquishes a recorded Treehouse lease whose ownership cannot\n" +
			"be established, either because the pool cannot be observed at all or because the pool holds no lease\n" +
			"identity to compare against. It needs an explicit task ID, it refuses any lease an observation can\n" +
			"still prove or disprove, and it never returns, prunes or deletes a worktree:\n" +
			"the worktree is left exactly as it is for the operator to reclaim through treehouse itself. The flag\n" +
			"only adds this worktree attestation and leaves every other reconciliation action unchanged, including\n" +
			"ordinary Herdr cleanup, which still requires proven ownership. An attempt that is still active is\n" +
			"reached only through a recorded teardown decision, the shape a teardown interrupted at the worktree\n" +
			"step leaves behind, while an attempt whose lifecycle is already terminal is eligible on its own once\n" +
			"its worktree resource is unsettled. No worktree of a live worker can be abandoned on either route,\n" +
			"because a provisioning or running attempt is skipped and the active attempt is reachable only through\n" +
			"that recorded decision.\n\n" +
			"--abandon-pane is the same attestation for a recorded Herdr pane identity: it relinquishes the claim\n" +
			"and closes no pane, tab or workspace. It needs an explicit task ID, and it refuses any pane identity an\n" +
			"observation confirms as Hand's or reports absent, because a confirmed pane is cleaned up and an absent\n" +
			"one needs no attestation. What is left is a recorded identity too incomplete to observe and one whose\n" +
			"workspace, tab or pane answers for something else, and those are the two states it covers. An active\n" +
			"attempt is reached only through a recorded teardown decision, so no pane of a live worker can be\n" +
			"abandoned.\n\n" +
			"--attempt-never-started attests to the one fact durable state cannot hold: that the worker of a\n" +
			"running Attempt took no turn at all, the shape a harness that stopped before its first turn leaves\n" +
			"behind. It needs an explicit task ID, and it refuses whenever anything on record disproves it: a\n" +
			"report line, a recorded pull request, merge or delivery, a reported state, a dirty worktree, or a\n" +
			"commit no remote-tracking ref reaches. It releases nothing itself. It records the teardown decision\n" +
			"`worker-never-started`, whose completion record claims no outcome about work, and the ordinary release\n" +
			"path then closes the pane and returns the worktree under its own unchanged guards, so a resource whose\n" +
			"ownership cannot be proven is still refused and still diagnosed.",
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
			if abandonPane && id == "" {
				return asPrecondition(runtime.Precondition(errors.New("--abandon-pane needs an explicit task ID")))
			}
			if attemptNeverStarted && id == "" {
				return asPrecondition(runtime.Precondition(errors.New("--attempt-never-started needs an explicit task ID")))
			}
			report, reconcileErr := runtime.New().Reconcile(runtime.ReconcileRequest{
				Context: cmd.Context(), Home: fleetHome, ID: id,
				AbandonWorktree: abandonWorktree, AbandonPane: abandonPane,
				AttemptNeverStarted: attemptNeverStarted,
			})
			if err := renderReconcileReport(cmd, report, asJSON); err != nil {
				return err
			}
			return reconcileReportError(report, reconcileErr)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON instead of TOON")
	cmd.Flags().BoolVar(&abandonWorktree, "abandon-worktree", false, "attest that Hand relinquishes an unprovable Treehouse lease without touching the worktree")
	cmd.Flags().BoolVar(&abandonPane, "abandon-pane", false, "attest that Hand relinquishes an unprovable Herdr pane identity without closing the pane")
	cmd.Flags().BoolVar(&attemptNeverStarted, "attempt-never-started", false, "attest that a running attempt's worker took no turn, recording an honest teardown decision")
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
		if result.Landing != "" {
			doc.Field("landing", result.Landing)
		}
		if result.Liveness != "" {
			doc.Field("liveness", result.Liveness)
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
