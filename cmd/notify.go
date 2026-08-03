package cmd

import (
	"errors"
	"fmt"

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

			// Not configured and delivery failure both mean nothing reached the
			// channel, so both are the same general error (exit 1) rather than the
			// historical exit-0 "notified" line: a notifier that can't tell "not
			// configured" from "delivered" converts an unreachable operator into an
			// apparently-reached one.
			if err := notify.Send(home, message); err != nil {
				if errors.Is(err, notify.ErrNotConfigured) {
					return fmt.Errorf("config/notify not set up, nothing delivered: %s", message)
				}
				return err
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "notified: %s\n", message); err != nil {
				return err
			}
			return nil
		},
	}
	return cmd
}
