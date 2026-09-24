package cmd

import (
	"strconv"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/store"
	"github.com/spf13/cobra"
)

func newTaskListCmd() *cobra.Command {
	var scope string
	cmd := &cobra.Command{
		Use: "list", Short: "Read canonical Task archive membership",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			homeDir, err := home.Resolve()
			if err != nil {
				return err
			}
			items, err := store.ListCanonicalV19Tasks(cmd.Context(), homeDir, scope)
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(items))
			for _, item := range items {
				archive := "none"
				if item.Archived {
					archive = "recorded"
				}
				rows = append(rows, []string{item.ID, item.ProjectID, strconv.FormatInt(item.Ordinal, 10),
					item.GoalDigest, item.Lifecycle, archive})
			}
			var doc axi.Doc
			doc.Field("scope", scope)
			doc.Rows("tasks", []string{"id", "project_id", "ordinal", "goal_digest", "lifecycle", "archive"}, rows)
			doc.Help("Task membership only. Use task show <id> for exact archive fact. Unresolved obligations are not projected here.")
			return doc.Render(cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "unarchived", "Task membership: unarchived, archived, or all")
	return cmd
}
