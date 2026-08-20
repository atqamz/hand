package cmd

import (
	"fmt"
	"time"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/state"
	"github.com/spf13/cobra"
)

func newAckCmd() *cobra.Command {
	var reason string

	cmd := &cobra.Command{
		Use:   "ack <id>",
		Short: "Record that a supervisor has acknowledged this task's report",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			release, err := state.Lock(home, "task:"+id)
			if err != nil {
				return fmt.Errorf("lock task %q: %w", id, err)
			}
			defer release()

			if _, err := state.Read(home, id); err != nil {
				return asPrecondition(err)
			}

			// Whatever is in the channel right now, this act consumed - the same completeness rule a
			// watcher's own announcement cursor follows, so a line still being appended is never claimed.
			cursor, err := state.CurrentReportCursor(home, id)
			if err != nil {
				return fmt.Errorf("read report %q: %w", id, err)
			}

			ackedAt := time.Now().UTC().Format(time.RFC3339)
			if err := state.SetTaskAcknowledgement(home, id, ackedAt, reason, cursor.Offset, cursor.Digest); err != nil {
				return fmt.Errorf("write task state: %w", err)
			}

			var doc axi.Doc
			doc.Field("id", id)
			doc.Field("result", "acknowledged")
			doc.Field("reason", orNone(reason))
			doc.Field("acknowledged_at", ackedAt)
			doc.Help("Run `hand status " + id + "` to confirm the unacknowledged flag is clear")
			return doc.Render(cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "", "why this report is considered handled")
	return cmd
}
