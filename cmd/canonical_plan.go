package cmd

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/store"
	"github.com/spf13/cobra"
)

func newProjectPolicyCmd() *cobra.Command {
	var input store.CanonicalV19PolicyRecordInput
	cmd := &cobra.Command{
		Use: "policy <id>", Short: "Record exact declared Project policy references",
		Long: "Record policy references using a caller-retained revision ID. All five reference flags are required; an explicit empty value records unconfigured policy and grants no exemption.\n" +
			"Use non-secret reference identifiers only. This command does not resolve definitions, validate capabilities or authorize acceptance.\n" +
			"Changing current policy requires --supersedes with the exact predecessor ID. Existing Plans retain their captured revision. Duplicate IDs refuse.",
		Args: usageArgs(cobra.ExactArgs(1)), PersistentPreRunE: canonicalSupervisorPreflight,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve()
			if err != nil {
				return err
			}
			input.ID = args[0]
			ordinal, digest, err := store.RecordCanonicalV19Policy(cmd.Context(), homeDir, input)
			if err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("policy_revision_id", input.ID)
			doc.Field("project_id", input.ProjectID)
			doc.Field("policy_digest", digest)
			doc.Int("ordinal", int(ordinal))
			return doc.Render(cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&input.ProjectID, "project-id", "", "Exact canonical Project ID")
	cmd.Flags().StringVar(&input.SupersedesID, "supersedes", "", "Exact current PolicyRevision ID; omit only for initial policy")
	for _, ref := range []struct {
		flag  string
		value *string
	}{
		{"worker-profile-ref", &input.WorkerProfileRef},
		{"qualification-policy-ref", &input.QualificationPolicyRef},
		{"integration-policy-ref", &input.IntegrationPolicyRef},
		{"production-policy-ref", &input.ProductionPolicyRef},
		{"publication-policy-ref", &input.PublicationPolicyRef},
	} {
		cmd.Flags().StringVar(ref.value, ref.flag, "", "Declared non-secret reference; empty means unconfigured")
		_ = cmd.MarkFlagRequired(ref.flag)
	}
	_ = cmd.MarkFlagRequired("project-id")
	return cmd
}

func newPlanCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "plan", Short: "Record immutable canonical Plans", PersistentPreRunE: canonicalSupervisorPreflight}
	for _, replan := range []bool{false, true} {
		var input store.CanonicalV19PlanCreateInput
		var predecessor string
		verb := "create"
		if replan {
			verb = "replan"
		}
		child := &cobra.Command{
			Use: verb + " <id>", Short: "Record an exact Task's immutable Plan meaning",
			Long: "Capture exact current WorkspaceBinding and PolicyRevision IDs with immutable intent, judgment, basis and brief.\n" +
				"Replan names the exact active predecessor and refuses while it has an active Attempt. No Attempt, resource or worker is created. Duplicate IDs refuse.",
			Args: usageArgs(cobra.ExactArgs(1)),
			RunE: func(cmd *cobra.Command, args []string) error {
				homeDir, err := home.Resolve()
				if err != nil {
					return err
				}
				if input.Basis == "" || input.Brief == "" {
					return fmt.Errorf("plan basis and brief must be explicit and nonempty")
				}
				input.ID = args[0]
				input.BriefDigest = fmt.Sprintf("%x", sha256.Sum256([]byte(input.Brief)))
				input.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
				var ordinal int64
				if replan {
					ordinal, err = store.ReplanCanonicalV19Plan(cmd.Context(), homeDir, store.CanonicalV19PlanReplanInput{
						PredecessorPlanID: predecessor, Successor: input, SupersededAt: input.CreatedAt,
					})
				} else {
					ordinal, err = store.CreateCanonicalV19RootPlan(cmd.Context(), homeDir, input)
				}
				if err != nil {
					return err
				}
				var doc axi.Doc
				doc.Field("plan_id", input.ID)
				doc.Field("task_id", input.TaskID)
				doc.Int("ordinal", int(ordinal))
				return doc.Render(cmd.OutOrStdout())
			},
		}
		for _, flag := range []struct {
			name, help string
			value      *string
		}{
			{"task-id", "Exact canonical Task ID", &input.TaskID},
			{"workspace-binding-id", "Exact current WorkspaceBinding ID", &input.WorkspaceBindingID},
			{"policy-revision-id", "Exact current PolicyRevision ID", &input.PolicyRevisionID},
			{"intent", "explore or execute", &input.Intent},
			{"judgment", "mechanical, bounded or substantial", &input.Judgment},
			{"basis", "Immutable planning basis", &input.Basis},
			{"brief", "Immutable work brief", &input.Brief},
		} {
			child.Flags().StringVar(flag.value, flag.name, "", flag.help)
			_ = child.MarkFlagRequired(flag.name)
		}
		if replan {
			child.Flags().StringVar(&predecessor, "predecessor", "", "Exact active predecessor Plan ID")
			_ = child.MarkFlagRequired("predecessor")
		}
		cmd.AddCommand(child)
	}
	cmd.AddCommand(&cobra.Command{
		Use: "abandon <id>", Short: "Abandon one exact active Plan without a successor",
		Long: "Terminalize an exact active Plan as abandoned. It refuses while the Plan has an active Attempt, unresolved external operation, open ExecutorBinding or open Repair.\n" +
			"The Task keeps its lifecycle but cannot receive another Plan; abandon the Task next. No Attempt, resource or worker is changed.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve()
			if err != nil {
				return err
			}
			at := time.Now().UTC().Format(time.RFC3339Nano)
			if err := store.AbandonCanonicalV19Plan(cmd.Context(), homeDir, args[0], at); err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("plan_id", args[0])
			doc.Field("lifecycle", "abandoned")
			doc.Field("terminal_at", at)
			return doc.Render(cmd.OutOrStdout())
		},
	})
	return cmd
}
