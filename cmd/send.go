package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/state"
	"github.com/spf13/cobra"
)

const sendComposerTimeout = 10 * time.Second

func newSendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send <id> <message>",
		Short: "Send a text message to a running worker's herdr pane",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			message := args[1]

			home, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}

			t, err := state.Read(home, id)
			if err != nil {
				return err
			}

			client := herdr.NewClient()
			pane, err := client.PaneGet(t.Herdr.PaneID)
			if err != nil {
				return fmt.Errorf("herdr pane %s not found: %w", t.Herdr.PaneID, err)
			}

			if pane.AgentStatus == herdr.StatusWorking {
				if err := client.WaitComposerEmpty(t.Herdr.PaneID, sendComposerTimeout); err != nil {
					return err
				}
			}

			if err := client.PaneSendText(t.Herdr.PaneID, message); err != nil {
				return fmt.Errorf("send message failed: %w", err)
			}
			if err := client.PaneSendKeys(t.Herdr.PaneID, "Enter"); err != nil {
				return fmt.Errorf("submit message failed: %w", err)
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "sent to %s\n", id); err != nil {
				return err
			}
			return nil
		},
	}
	return cmd
}
