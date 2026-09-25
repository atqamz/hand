package cmd

import (
	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/store"
	"github.com/spf13/cobra"
)

func newTaskArchiveCmd() *cobra.Command {
	var input store.CanonicalV19TaskArchiveInput
	cmd := &cobra.Command{
		Use: "archive <task-id>", Short: "Archive an exact terminal Task whose resources are released",
		Long: "Append one immutable archive fact for a terminal canonical Task. Every external operation in its lineage must be resolved, and every Worktree, Session and Executor binding must have its durable release or termination fact. Archive reads no Git, filesystem or provider state. Retain every flag for exact replay after a lost response.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := canonicalTimestamp(input.ArchivedAt); err != nil {
				return err
			}
			if err := canonicalEvidenceDigest(input.EvidenceDigest); err != nil {
				return err
			}
			homeDir, err := home.Resolve()
			if err != nil {
				return err
			}
			input.TaskID = args[0]
			if err := store.ArchiveCanonicalV19Task(cmd.Context(), homeDir, input); err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("task_id", input.TaskID)
			doc.Field("archived_at", input.ArchivedAt)
			doc.Field("evidence_digest", input.EvidenceDigest)
			return doc.Render(cmd.OutOrStdout())
		},
	}
	for _, flag := range []struct {
		name, help string
		value      *string
	}{
		{"actor-kind", "operator or supervisor", &input.ActorKind},
		{"actor-ref", "Exact actor reference", &input.ActorRef},
		{"archived-at", "Caller-retained RFC3339 archive timestamp", &input.ArchivedAt},
		{"reason", "Bounded archive reason", &input.Reason},
		{"evidence-digest", "Exact lowercase SHA-256 of retained archive evidence", &input.EvidenceDigest},
	} {
		cmd.Flags().StringVar(flag.value, flag.name, "", flag.help)
		_ = cmd.MarkFlagRequired(flag.name)
	}
	return cmd
}
