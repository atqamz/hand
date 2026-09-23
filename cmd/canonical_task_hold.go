package cmd

import (
	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/store"
	"github.com/spf13/cobra"
)

func newTaskHoldCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "hold", Short: "Record exact Task-level deferral and resolution"}
	cmd.AddCommand(newTaskHoldCreateCmd(), newTaskHoldShowCmd(), newTaskHoldResolveCmd())
	return cmd
}

func newTaskHoldCreateCmd() *cobra.Command {
	var input store.CanonicalV19TaskHoldCreateInput
	cmd := &cobra.Command{
		Use: "create <id>", Short: "Record one TaskHold with optional exact typed relationships",
		Long: "Record genuine Task-level deferral. Provider limits belong to AttemptBackoff. Retain the exact Hold ID; duplicate creation refuses. After a lost response, inspect that ID with task hold show. A Decision link does not answer the question; an Answer never automatically releases this Hold.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := canonicalTimestamp(input.CreatedAt); err != nil {
				return err
			}
			if input.RecheckNotBefore != "" {
				if err := canonicalTimestamp(input.RecheckNotBefore); err != nil {
					return err
				}
			}
			if err := canonicalEvidenceDigest(input.EvidenceDigest); err != nil {
				return err
			}
			homeDir, err := home.Resolve()
			if err != nil {
				return err
			}
			input.ID = args[0]
			ordinal, err := store.CreateCanonicalV19TaskHold(cmd.Context(), homeDir, input)
			if err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("hold_id", input.ID)
			doc.Field("task_id", input.TaskID)
			doc.Int("ordinal", int(ordinal))
			return doc.Render(cmd.OutOrStdout())
		},
	}
	for _, flag := range []struct {
		name, help string
		value      *string
	}{
		{"task-id", "Exact owning Task ID", &input.TaskID},
		{"kind", "operator or blocked; the Task-level reason for deferral", &input.Kind},
		{"reason", "Immutable reason for deferral", &input.Reason},
		{"evidence-digest", "Exact lowercase SHA-256 of externally retained deferral evidence", &input.EvidenceDigest},
		{"created-at", "Caller-retained RFC3339 creation timestamp", &input.CreatedAt},
	} {
		cmd.Flags().StringVar(flag.value, flag.name, "", flag.help)
		_ = cmd.MarkFlagRequired(flag.name)
	}
	cmd.Flags().StringVar(&input.BlockedOnTaskID, "blocked-on-task-id", "", "Exact dependency Task ID for a blocked Hold")
	cmd.Flags().StringVar(&input.DecisionID, "decision-id", "", "Exact Decision ID when deferral and a question coexist")
	cmd.Flags().StringVar(&input.RecheckNotBefore, "recheck-not-before", "", "Optional RFC3339 recheck boundary; not automatic release")
	return cmd
}

func newTaskHoldShowCmd() *cobra.Command {
	return &cobra.Command{
		Use: "show <id>", Short: "Read exact TaskHold history without resolving it",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve()
			if err != nil {
				return err
			}
			view, err := store.ReadCanonicalV19TaskHold(cmd.Context(), homeDir, args[0])
			if err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("hold_id", view.Hold.ID)
			doc.Field("task_id", view.Hold.TaskID)
			doc.Int("ordinal", int(view.Ordinal))
			doc.Field("kind", view.Hold.Kind)
			doc.Field("reason", view.Hold.Reason)
			doc.Field("evidence_digest", view.Hold.EvidenceDigest)
			doc.Field("created_at", view.Hold.CreatedAt)
			doc.Field("blocked_on_task_id", valueOrNone(view.Hold.BlockedOnTaskID))
			doc.Field("decision_id", valueOrNone(view.Hold.DecisionID))
			doc.Field("recheck_not_before", valueOrNone(view.Hold.RecheckNotBefore))
			doc.Bool("owner_current", view.OwnerCurrent)
			doc.Bool("unresolved", view.Resolution == nil)
			if view.Resolution != nil {
				doc.Field("resolution", view.Resolution.Resolution)
				doc.Field("resolved_at", view.Resolution.ResolvedAt)
				doc.Field("resolution_evidence_digest", view.Resolution.EvidenceDigest)
			}
			return doc.Render(cmd.OutOrStdout())
		},
	}
}

func newTaskHoldResolveCmd() *cobra.Command {
	var input store.CanonicalV19TaskHoldResolveInput
	cmd := &cobra.Command{
		Use: "resolve <id>", Short: "Resolve one exact unresolved TaskHold",
		Long: "Append immutable resolution evidence for this exact Hold only. Repeating resolution refuses; after a lost response inspect task hold show. Resolution does not resolve another Hold, answer a Decision, acknowledge a Worker or satisfy a Task.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := canonicalTimestamp(input.ResolvedAt); err != nil {
				return err
			}
			if err := canonicalEvidenceDigest(input.EvidenceDigest); err != nil {
				return err
			}
			homeDir, err := home.Resolve()
			if err != nil {
				return err
			}
			input.HoldID = args[0]
			if err := store.ResolveCanonicalV19TaskHold(cmd.Context(), homeDir, input); err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("hold_id", input.HoldID)
			doc.Field("resolution", input.Resolution)
			return doc.Render(cmd.OutOrStdout())
		},
	}
	for _, flag := range []struct {
		name, help string
		value      *string
	}{
		{"resolution", "released, cancelled or superseded", &input.Resolution},
		{"resolved-at", "Caller-retained RFC3339 resolution timestamp", &input.ResolvedAt},
		{"evidence-digest", "Exact lowercase SHA-256 of externally retained resolution evidence", &input.EvidenceDigest},
	} {
		cmd.Flags().StringVar(flag.value, flag.name, "", flag.help)
		_ = cmd.MarkFlagRequired(flag.name)
	}
	return cmd
}
