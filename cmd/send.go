package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/state"
	"github.com/spf13/cobra"
)

func newSendCmd() *cobra.Command {
	var filePath string
	var wait string

	cmd := &cobra.Command{
		Use:   "send <id> [message]",
		Short: "Send a text message to a running worker's herdr pane",
		// filePath, not Flags().Changed("file"): --file "" is no message source at
		// all, and keying off Changed would accept one positional argument while
		// RunE still reads args[1].
		Args: usageArgs(func(c *cobra.Command, args []string) error {
			want := 2
			if filePath != "" {
				want = 1
			}
			return cobra.ExactArgs(want)(c, args)
		}),
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

			// Serializes concurrent sends to the same task: two retry loops racing against the same busy
			// composer is the exact hazard atqamz/secondhand#102 traced a lost steer to. A second send waits
			// behind the first, its own --wait clock starting only once it holds this lock.
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
					// The pane stopped answering mid-wait (agent died, tab closed):
					// no retry can succeed, so this is the same exit-1 outcome as the
					// PaneGet above, not the retryable busy-composer one below.
					if !errors.Is(waitErr, herdr.ErrComposerBusyTimeout) {
						return fmt.Errorf("herdr pane %s not found: %w", t.Herdr.PaneID, waitErr)
					}
					if err := recordUndeliveredSend(home, id, message); err != nil {
						return fmt.Errorf("%w; record undelivered send: %w", waitErr, err)
					}
					// Code 6: the composer stayed busy for the whole --wait bound, so the message never reached
					// the pane - a transient a caller can retry, distinct from the exit-1 paths above and below
					// that can never succeed (no such pane, herdr erroring). 4 and 5 are hand watch --until-event's.
					return &ExitError{Err: fmt.Errorf("%w, message recorded as undelivered", waitErr), Code: 6}
				}
			}

			// Both delivery failures leave the steer undemonstrated in the pane -
			// text that never left, and text sitting unsubmitted in the composer -
			// so both owe the operator the same durable trace the wait bound does.
			if err := client.PaneSendText(t.Herdr.PaneID, message); err != nil {
				return withUndeliveredSend(home, id, message, fmt.Errorf("send message failed: %w", err))
			}
			if err := client.PaneSendKeys(t.Herdr.PaneID, "Enter"); err != nil {
				return withUndeliveredSend(home, id, message, fmt.Errorf("submit message failed: %w", err))
			}

			if err := clearUndeliveredSend(home, id); err != nil {
				// The message is already in the pane, so failing here would invite a
				// retry that double-sends the steer; a stale trace is the lesser harm.
				if _, printErr := fmt.Fprintf(cmd.ErrOrStderr(), "warning: clear undelivered send trace: %v\n", err); printErr != nil {
					return printErr
				}
			}

			var doc axi.Doc
			doc.Field("id", id)
			doc.Field("result", "sent")
			doc.Int("chars", len([]rune(message)))
			doc.Help("The pane has the message; run `hand status " + id + "` to read what it does with it")
			return doc.Render(cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&filePath, "file", "", "read the message from this file instead of the positional argument")
	cmd.Flags().StringVar(&wait, "wait", "", "how long to wait for a busy composer to free before giving up (default: config/send-wait, or 2m)")
	return cmd
}

// Records the trace alongside a delivery failure, keeping the cause as the returned error so the exit
// code of the failing path is unchanged.
func withUndeliveredSend(home, id, message string, cause error) error {
	if err := recordUndeliveredSend(home, id, message); err != nil {
		return fmt.Errorf("%w; record undelivered send: %w", cause, err)
	}
	return cause
}

// Durably records the message hand send could not demonstrably deliver, so an operator or worker learns
// the steer never arrived instead of it vanishing with the process that attempted it.
func recordUndeliveredSend(home, id, message string) error {
	return setUndeliveredSend(home, id, message, time.Now().UTC().Format(time.RFC3339))
}

// Runs after every send that actually reaches the pane, whatever message that send carries: the trace's
// job is telling the operator their last attempt did not land, and any successful send moots it.
func clearUndeliveredSend(home, id string) error {
	return setUndeliveredSend(home, id, "", "")
}

func setUndeliveredSend(home, id, message, at string) error {
	// A short-lived task-row lock, separate from the send lock the command holds across its whole wait,
	// so a long busy wait never blocks an unrelated reader like hand watch or hand status.
	release, err := state.Lock(home, "task:"+id)
	if err != nil {
		return fmt.Errorf("lock task %q: %w", id, err)
	}
	defer release()

	t, err := state.Read(home, id)
	if err != nil {
		return err
	}
	if t.SendUndeliveredMessage == message && t.SendUndeliveredAt == at {
		return nil
	}
	t.SendUndeliveredMessage = message
	t.SendUndeliveredAt = at
	return state.Write(home, t)
}
