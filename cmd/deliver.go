package cmd

import (
	"fmt"
	"time"

	"github.com/atqamz/secondhand/internal/axi"
	"github.com/atqamz/secondhand/internal/home"
	"github.com/atqamz/secondhand/internal/state"
	"github.com/spf13/cobra"
)

func newDeliverCmd() *cobra.Command {
	var reason string

	cmd := &cobra.Command{
		Use:   "deliver <id>",
		Short: "Record that a task's work is handed off and landing it is someone else's call",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if reason == "" {
				return &ExitError{Err: fmt.Errorf("--reason is required"), Code: 2}
			}

			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			release, err := state.Lock(home, "task:"+id)
			if err != nil {
				return fmt.Errorf("lock task %q: %w", id, err)
			}
			defer release()

			t, err := state.Read(home, id)
			if err != nil {
				return asPrecondition(err)
			}

			// Re-running with a new reason is a correction, not a conflict: nothing
			// downstream has consumed the mark until teardown reads it, and the last
			// word on what was delivered is the one worth keeping.
			t.DeliveredAt = time.Now().UTC().Format(time.RFC3339)
			t.DeliveredReason = reason
			if err := state.Write(home, t); err != nil {
				return fmt.Errorf("write task state: %w", err)
			}

			var doc axi.Doc
			doc.Field("id", id)
			doc.Field("result", "delivered")
			doc.Field("reason", reason)
			doc.Field("delivered", t.DeliveredAt)
			doc.Help("Run `hand teardown " + id + "` once the work is landed to release the worktree and pane")
			return doc.Render(cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "", "what was delivered and who decides whether it lands")
	return cmd
}
