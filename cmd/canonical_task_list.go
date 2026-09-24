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
	var after string
	var limit int
	cmd := &cobra.Command{
		Use: "list", Short: "Read a bounded page of canonical Task archive membership",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			homeDir, err := home.Resolve()
			if err != nil {
				return err
			}
			page, err := store.ListCanonicalV19Tasks(cmd.Context(), homeDir, scope, after, limit)
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(page.Items))
			for _, item := range page.Items {
				archive := "none"
				if item.Archived {
					archive = "recorded"
				}
				rows = append(rows, []string{item.ID, item.ProjectID, strconv.FormatInt(item.Ordinal, 10),
					item.GoalDigest, item.Lifecycle, archive})
			}
			var doc axi.Doc
			doc.Field("scope", scope)
			doc.Field("limit", strconv.Itoa(limit))
			doc.Field("next_after", page.NextAfter)
			doc.Rows("tasks", []string{"id", "project_id", "ordinal", "goal_digest", "lifecycle", "archive"}, rows)
			doc.Help("Task ID order; pass next_after as --after for the next page. Use task show <id> for exact archive fact. Unresolved obligations are not projected here.")
			return doc.Render(cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "unarchived", "Task membership: unarchived, archived, or all")
	cmd.Flags().StringVar(&after, "after", "", "Exclusive Task ID cursor from the preceding page")
	cmd.Flags().IntVar(&limit, "limit", 100, "Maximum Tasks per page (1–1000)")
	return cmd
}
