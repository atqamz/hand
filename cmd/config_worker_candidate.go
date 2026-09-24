package cmd

import (
	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/routing"
	"github.com/atqamz/hand/internal/store"
	"github.com/spf13/cobra"
)

func newConfigWorkerCandidateCmd() *cobra.Command {
	var profile, harness, model, effort, planID string
	cmd := &cobra.Command{
		Use: "candidate [<intent> <judgment>]", Short: "Preview one unqualified Worker Route candidate",
		Long: "Resolve the selected Worker Profile and explicit field overrides from the exact configured policy bytes. With --plan-id, derive intent and judgment from one exact active canonical Plan instead of caller-supplied axes. This read does not verify live capability or availability, apply fallback, or create an Attempt.",
		Args: usageArgs(func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("plan-id") {
				return cobra.NoArgs(cmd, args)
			}
			return cobra.ExactArgs(2)(cmd, args)
		}),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve()
			if err != nil {
				return err
			}
			var plan store.CanonicalV19CurrentPlanRouting
			intent, judgment := "", ""
			if cmd.Flags().Changed("plan-id") {
				plan, err = store.ReadCanonicalV19CurrentPlanRouting(cmd.Context(), homeDir, planID)
				if err != nil {
					return err
				}
				intent, judgment = plan.Intent, plan.Judgment
			} else {
				intent, judgment = args[0], args[1]
			}
			var overrides routing.WorkerCandidateOverrides
			var requested [][]string
			for _, flag := range []struct {
				name  string
				value *string
				dest  **string
			}{
				{"profile-override", &profile, &overrides.ProfileOverride},
				{"harness-override", &harness, &overrides.HarnessOverride},
				{"model-override", &model, &overrides.ModelOverride},
				{"effort-override", &effort, &overrides.EffortOverride},
			} {
				if cmd.Flags().Changed(flag.name) {
					*flag.dest = flag.value
					requested = append(requested, []string{flag.name, *flag.value})
				}
			}
			candidate, err := routing.ResolveWorkerCandidate(homeDir, intent, judgment, overrides)
			if err != nil {
				return err
			}
			var doc axi.Doc
			if plan.ID != "" {
				doc.Field("plan_id", plan.ID)
				doc.Field("plan_policy_revision_id", plan.PolicyRevisionID)
			}
			doc.Field("intent", intent)
			doc.Field("judgment", judgment)
			doc.Field("profile", candidate.Profile.Name)
			doc.Field("harness", candidate.Profile.Harness)
			doc.Field("model", candidate.Profile.Model)
			doc.Field("effort", candidate.Profile.Effort)
			doc.Field("policy_witness", candidate.PolicyWitness)
			doc.Rows("requested_overrides", []string{"field", "value"}, requested)
			doc.Field("qualification", "unverified")
			doc.Bool("attempt_created", false)
			doc.Help("No live capability, availability, quota, fallback, or future Attempt currentness was verified.")
			return doc.Render(cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&profile, "profile-override", "", "Select an approved Worker Profile by name")
	cmd.Flags().StringVar(&harness, "harness-override", "", "Replace the selected Worker Harness")
	cmd.Flags().StringVar(&model, "model-override", "", "Replace or explicitly clear the selected model")
	cmd.Flags().StringVar(&effort, "effort-override", "", "Replace or explicitly clear the selected effort")
	cmd.Flags().StringVar(&planID, "plan-id", "", "Preview the exact active canonical Plan's Worker Route; omit intent and judgment arguments")
	return cmd
}
