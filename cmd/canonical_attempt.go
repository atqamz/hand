package cmd

import (
	"context"
	"errors"
	"time"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/routing"
	"github.com/atqamz/hand/internal/store"
	"github.com/spf13/cobra"
)

func newAttemptCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "attempt", Short: "Record canonical Attempts with resolved Worker provenance", PersistentPreRunE: canonicalSupervisorPreflight}
	for _, retry := range []bool{false, true} {
		var planID, predecessor string
		verb := "create"
		if retry {
			verb = "retry"
		}
		child := &cobra.Command{
			Use: verb + " <id>", Short: "Record one Attempt under an exact active Plan",
			Long: "Resolve the exact active Plan's Worker Route from the configured worker policy and freeze the selected Profile, Worker Harness, model, effort and requested overrides on a new Attempt.\n" +
				"The worker policy witness is rechecked at commit inside the Attempt transaction; a policy changed between resolution and that recheck refuses with no Attempt. Create requires a Plan with no Attempt history; retry names the exact latest terminal predecessor.\n" +
				"No worktree, session or worker is started. Duplicate IDs refuse.",
			Args: usageArgs(cobra.ExactArgs(1)),
		}
		readOverrides := bindWorkerCandidateOverrides(child)
		child.RunE = func(cmd *cobra.Command, args []string) error {
			if retry && predecessor == "" {
				return usageValue(true, errors.New("retry --predecessor must name the exact latest Attempt"))
			}
			homeDir, err := home.Resolve()
			if err != nil {
				return err
			}
			overrides, requested := readOverrides()
			input, candidate, err := resolveCanonicalAttempt(cmd.Context(), homeDir, args[0], planID, predecessor, overrides)
			if err != nil {
				return err
			}
			ordinal, err := store.CreateCanonicalV19Attempt(cmd.Context(), homeDir, input)
			if err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("attempt_id", input.ID)
			doc.Field("plan_id", input.PlanID)
			doc.Int("ordinal", int(ordinal))
			doc.Field("profile", candidate.Profile.Name)
			doc.Field("harness", candidate.Profile.Harness)
			doc.Field("model", candidate.Profile.Model)
			doc.Field("effort", candidate.Profile.Effort)
			doc.Field("session_adapter", input.SessionAdapterRef)
			doc.Field("policy_witness", candidate.PolicyWitness)
			doc.Rows("requested_overrides", []string{"field", "value"}, requested)
			doc.Bool("worker_launched", false)
			return doc.Render(cmd.OutOrStdout())
		}
		child.Flags().StringVar(&planID, "plan-id", "", "Exact active canonical Plan ID")
		_ = child.MarkFlagRequired("plan-id")
		if retry {
			child.Flags().StringVar(&predecessor, "predecessor", "", "Exact latest terminal Attempt ID of the Plan")
			_ = child.MarkFlagRequired("predecessor")
		}
		cmd.AddCommand(child)
	}
	return cmd
}

func resolveCanonicalAttempt(ctx context.Context, homeDir, id, planID, predecessor string, overrides routing.WorkerCandidateOverrides) (store.CanonicalV19AttemptCreateInput, routing.WorkerCandidate, error) {
	plan, err := store.ReadCanonicalV19CurrentPlanRouting(ctx, homeDir, planID)
	if err != nil {
		return store.CanonicalV19AttemptCreateInput{}, routing.WorkerCandidate{}, err
	}
	candidate, err := routing.ResolveWorkerCandidate(homeDir, plan.Intent, plan.Judgment, overrides)
	if err != nil {
		return store.CanonicalV19AttemptCreateInput{}, routing.WorkerCandidate{}, err
	}
	return store.CanonicalV19AttemptCreateInput{
		ID:                   id,
		PlanID:               plan.ID,
		PredecessorAttemptID: predecessor,
		RequirePolicyWitnessCurrent: func() error {
			return routing.RequireWorkerPolicyWitness(homeDir, candidate.PolicyWitness)
		},
		ProfileOverride:   overrides.ProfileOverride,
		HarnessOverride:   overrides.HarnessOverride,
		ModelOverride:     overrides.ModelOverride,
		EffortOverride:    overrides.EffortOverride,
		WorkerHarnessRef:  candidate.Profile.Harness,
		WorkerProfileRef:  candidate.Profile.Name,
		ModelRef:          candidate.Profile.Model,
		EffortRef:         candidate.Profile.Effort,
		SessionAdapterRef: store.CanonicalV19HerdrSessionAdapterRef,
		CreatedAt:         time.Now().UTC().Format(time.RFC3339Nano),
	}, candidate, nil
}
