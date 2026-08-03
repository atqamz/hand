package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/atqamz/secondhand/internal/home"
	"github.com/atqamz/secondhand/internal/state"
	"github.com/spf13/cobra"
)

func newSendCmd() *cobra.Command {
	var filePath string
	var wait string

	cmd := &cobra.Command{
		Use:   "send <id> [message]",
		Short: "Send a text message to a running worker's herdr pane",
		Args: func(c *cobra.Command, args []string) error {
			want := 2
			if c.Flags().Changed("file") {
				want = 1
			}
			if len(args) != want {
				return &ExitError{Err: fmt.Errorf("accepts %d arg(s), received %d", want, len(args)), Code: 2}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			var message string
			if filePath != "" {
				data, err := os.ReadFile(filePath)
				if err != nil {
					return fmt.Errorf("read --file %q: %w", filePath, err)
				}
				message = strings.TrimRight(string(data), "\n")
			} else {
				message = args[1]
			}

			waitFromFlag := wait != ""
			if !waitFromFlag {
				wait = configDefault(home, "send-wait", "2m")
			}
			waitDuration, err := time.ParseDuration(wait)
			if err != nil {
				return usageValue(waitFromFlag, fmt.Errorf("invalid wait duration %q: %w", wait, err))
			}

			// Serializes concurrent sends to the same task: two retry loops racing
			// against the same busy composer is the exact hazard atqamz/secondhand#102
			// traced a lost steer to. A second send now waits behind the first
			// rather than polling the same pane at the same time, its own
			// --wait clock starting only once it holds this lock.
			release, err := state.Lock(home, "send:"+id)
			if err != nil {
				return fmt.Errorf("lock send %q: %w", id, err)
			}
			defer release()

			t, err := state.Read(home, id)
			if err != nil {
				return asPrecondition(err)
			}

			client := herdr.NewClient()
			pane, err := client.PaneGet(t.Herdr.PaneID)
			if err != nil {
				return fmt.Errorf("herdr pane %s not found: %w", t.Herdr.PaneID, err)
			}

			if pane.AgentStatus == herdr.StatusWorking {
				if waitErr := client.WaitComposerEmpty(t.Herdr.PaneID, waitDuration); waitErr != nil {
					if err := recordUndeliveredSend(home, id, message); err != nil {
						return fmt.Errorf("%w; record undelivered send: %w", waitErr, err)
					}
					// Code 6: the composer stayed busy for the whole --wait bound, so
					// the message never reached the pane - a transient state a caller
					// can retry, distinct from the exit-1 paths above and below that
					// mean the send can never succeed (no such pane, herdr itself
					// erroring). 4 and 5 are reserved to hand watch --until-event.
					return &ExitError{Err: fmt.Errorf("composer still busy after %s, message recorded as undelivered: %w", waitDuration, waitErr), Code: 6}
				}
			}

			if err := client.PaneSendText(t.Herdr.PaneID, message); err != nil {
				return fmt.Errorf("send message failed: %w", err)
			}
			if err := client.PaneSendKeys(t.Herdr.PaneID, "Enter"); err != nil {
				return fmt.Errorf("submit message failed: %w", err)
			}

			if err := clearUndeliveredSend(home, id); err != nil {
				return fmt.Errorf("clear undelivered send trace: %w", err)
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "sent to %s\n", id); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&filePath, "file", "", "read the message from this file instead of the positional argument")
	cmd.Flags().StringVar(&wait, "wait", "", "how long to wait for a busy composer to free before giving up (default: config/send-wait, or 2m)")
	return cmd
}

// recordUndeliveredSend durably records the message hand send gave up on, so
// an operator or worker learns the steer never arrived instead of it
// vanishing with the process that attempted it. A short-lived task-row lock,
// separate from the send lock held for the whole wait above, so a long busy
// wait never blocks an unrelated reader like hand watch or hand status.
func recordUndeliveredSend(home, id, message string) error {
	release, err := state.Lock(home, "task:"+id)
	if err != nil {
		return fmt.Errorf("lock task %q: %w", id, err)
	}
	defer release()

	t, err := state.Read(home, id)
	if err != nil {
		return err
	}
	t.SendUndeliveredMessage = message
	t.SendUndeliveredAt = time.Now().UTC().Format(time.RFC3339)
	return state.Write(home, t)
}

// clearUndeliveredSend runs after every send that actually reaches the pane,
// whatever message that send carries: the trace's job is telling the operator
// their last attempt did not land, and any successful send moots it.
func clearUndeliveredSend(home, id string) error {
	release, err := state.Lock(home, "task:"+id)
	if err != nil {
		return fmt.Errorf("lock task %q: %w", id, err)
	}
	defer release()

	t, err := state.Read(home, id)
	if err != nil {
		return err
	}
	if t.SendUndeliveredMessage == "" && t.SendUndeliveredAt == "" {
		return nil
	}
	t.SendUndeliveredMessage = ""
	t.SendUndeliveredAt = ""
	return state.Write(home, t)
}
