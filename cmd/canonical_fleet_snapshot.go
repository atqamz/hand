package cmd

import (
	"strconv"

	"github.com/atqamz/hand/internal/attention"
	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/store"
	"github.com/spf13/cobra"
)

func newCanonicalFleetSnapshotCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "snapshot",
		Short:             "Read canonical Fleet and active lineage with partial coverage",
		Args:              usageArgs(cobra.NoArgs),
		PersistentPreRunE: canonicalSupervisorPreflight,
		RunE: func(cmd *cobra.Command, _ []string) error {
			homeDir, err := home.Resolve()
			if err != nil {
				return err
			}
			snapshot, err := store.ReadCanonicalV19FleetSnapshot(cmd.Context(), homeDir)
			if err != nil {
				return err
			}
			partialAttention := attention.DeriveCanonicalPartial(snapshot)
			var doc axi.Doc
			doc.Field("snapshot_schema", snapshot.Schema)
			doc.Field("completeness", snapshot.Completeness)
			doc.Field("fleet_id", snapshot.FleetID)
			doc.Field("db_read_at", snapshot.DBReadAt)
			doc.Field("attention", "unknown")
			projects := make([][]string, 0, len(snapshot.Projects))
			var tasks, plans, attempts [][]string
			for _, project := range snapshot.Projects {
				workspaceID, policyID := "none", "none"
				if project.Workspace != nil {
					workspaceID = project.Workspace.ID
				}
				if project.Policy != nil {
					policyID = project.Policy.ID
				}
				projects = append(projects, []string{project.ID, strconv.FormatInt(project.Ordinal, 10), project.DisplayName,
					valueOrNone(project.RetiredAt), workspaceID, policyID})
				for _, task := range project.Tasks {
					planID := "none"
					if task.Plan != nil {
						planID = task.Plan.ID
					}
					tasks = append(tasks, []string{task.ID, project.ID, strconv.FormatInt(task.Ordinal, 10), task.GoalDigest, planID})
					if task.Plan == nil {
						continue
					}
					plan := task.Plan
					attemptID := "none"
					if plan.Attempt != nil {
						attemptID = plan.Attempt.ID
					}
					plans = append(plans, []string{plan.ID, task.ID, strconv.FormatInt(plan.Ordinal, 10), plan.LineageKind,
						plan.Intent, plan.Judgment, plan.BriefDigest, plan.WorkspaceBindingID, plan.PolicyRevisionID, attemptID})
					if plan.Attempt != nil {
						attempt := plan.Attempt
						attempts = append(attempts, []string{attempt.ID, plan.ID, strconv.FormatInt(attempt.Ordinal, 10),
							attempt.WorkerHarnessRef, attempt.WorkerProfileRef, attempt.ModelRef, attempt.EffortRef, attempt.SessionAdapterRef})
					}
				}
			}
			doc.Rows("projects", []string{"id", "ordinal", "name", "retired_at", "workspace_binding_id", "policy_revision_id"}, projects)
			doc.Rows("tasks", []string{"id", "project_id", "ordinal", "goal_digest", "active_plan_id"}, tasks)
			doc.Rows("plans", []string{"id", "task_id", "ordinal", "lineage_kind", "intent", "judgment", "brief_digest", "workspace_binding_id", "policy_revision_id", "active_attempt_id"}, plans)
			doc.Rows("attempts", []string{"id", "plan_id", "ordinal", "worker_harness_ref", "worker_profile_ref", "model_ref", "effort_ref", "session_adapter_ref"}, attempts)
			inputs := make([][]string, 0, len(snapshot.UnacknowledgedInputs))
			for _, input := range snapshot.UnacknowledgedInputs {
				inputs = append(inputs, []string{input.ID, input.AttemptID, input.ExecutorBindingID,
					strconv.FormatInt(input.Ordinal, 10), input.OriginKind, input.PayloadDigest, input.CreatedAt})
			}
			doc.Rows("unacknowledged_inputs", []string{"id", "attempt_id", "executor_binding_id", "ordinal", "origin_kind", "payload_digest", "created_at"}, inputs)
			operations := make([][]string, 0, len(snapshot.UnresolvedOperations))
			for _, operation := range snapshot.UnresolvedOperations {
				operations = append(operations, []string{operation.ID, operation.Kind, operation.State, operation.ProjectID,
					valueOrNone(operation.TaskID), valueOrNone(operation.PlanID), valueOrNone(operation.AttemptID),
					operation.ScopeKind, operation.ScopeKey, valueOrNone(operation.SessionBindingID),
					valueOrNone(operation.ExecutorBindingID), strconv.FormatInt(operation.PendingThroughOrdinal, 10)})
			}
			doc.Rows("unresolved_operations", []string{"id", "kind", "state", "project_id", "task_id", "plan_id", "attempt_id", "scope_kind", "scope_key", "session_binding_id", "executor_binding_id", "pending_through_ordinal"}, operations)
			items := make([][]string, 0, len(partialAttention.Items))
			for _, item := range partialAttention.Items {
				items = append(items, []string{strconv.Itoa(item.Priority), item.Code, item.EvidenceID, valueOrNone(item.OperationKind),
					valueOrNone(item.OperationState), valueOrNone(item.ReportState), item.ProjectID, valueOrNone(item.TaskID), valueOrNone(item.PlanID), valueOrNone(item.AttemptID),
					valueOrNone(item.SessionBindingID), valueOrNone(item.ExecutorBindingID)})
			}
			doc.Rows("partial_attention_items", []string{"priority", "code", "evidence_id", "operation_kind", "operation_state", "report_state", "project_id", "task_id", "plan_id", "attempt_id", "session_binding_id", "executor_binding_id"}, items)
			doc.Help("Partial read: Attention covers unresolved external operations, current unacknowledged WorkerInput under an exact submitted or uncertain WorkerWake, and the latest unacknowledged handling-worthy WorkerReport on an active Attempt. Input, wake, and report remain separate; no acknowledgement or action is implied. Receipts, broader report history, holds, decisions, full history and external observations are not projected.")
			return doc.Render(cmd.OutOrStdout())
		},
	}
}
