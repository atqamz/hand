package cmd

import (
	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/store"
	"github.com/spf13/cobra"
)

func newTaskShowCmd() *cobra.Command {
	return &cobra.Command{
		Use: "show <task-id>", Short: "Read one exact canonical Task and its archive fact",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve()
			if err != nil {
				return err
			}
			view, err := store.ReadCanonicalV19Task(cmd.Context(), homeDir, args[0])
			if err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("task_id", view.ID)
			doc.Field("project_id", view.ProjectID)
			doc.Int("ordinal", int(view.Ordinal))
			doc.Field("goal", view.Goal)
			doc.Field("goal_digest", view.GoalDigest)
			doc.Field("supersedes_task_id", valueOrNone(view.SupersedesTaskID))
			doc.Field("lifecycle", view.Lifecycle)
			doc.Field("created_at", view.CreatedAt)
			doc.Field("terminal_at", valueOrNone(view.TerminalAt))
			if view.Archive == nil {
				doc.Field("archive", "none")
			} else {
				doc.Field("archive", "recorded")
				doc.Field("actor_kind", view.Archive.ActorKind)
				doc.Field("actor_ref", view.Archive.ActorRef)
				doc.Field("archived_at", view.Archive.ArchivedAt)
				doc.Field("reason", view.Archive.Reason)
				doc.Field("evidence_digest", view.Archive.EvidenceDigest)
			}
			doc.Help("Exact Task and archive fact only. Plan, Attempt, resource, effect and Attention history are not projected here.")
			return doc.Render(cmd.OutOrStdout())
		},
	}
}
