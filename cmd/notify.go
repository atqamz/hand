package cmd

import (
	"errors"
	"fmt"

	"github.com/atqamz/secondhand/internal/axi"
	"github.com/atqamz/secondhand/internal/home"
	"github.com/atqamz/secondhand/internal/notify"
	"github.com/spf13/cobra"
)

func newNotifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notify <message>",
		Short: "Send an out-of-band notification",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			message := args[0]
			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			// config/notify absent/empty and a failed send are both exit 1, never
			// the old exit-0 "notified" line - see SPECS.md's hand notify "Errors"
			// for why.
			if err := notify.Send(home, message); err != nil {
				if errors.Is(err, notify.ErrNotConfigured) {
					return fmt.Errorf("config/notify not set up, nothing delivered: %s", message)
				}
				return err
			}

			var doc axi.Doc
			doc.Field("result", "notified")
			doc.Field("message", message)
			return doc.Render(cmd.OutOrStdout())
		},
	}
	return cmd
}
