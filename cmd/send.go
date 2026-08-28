package cmd

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/steering"
	"github.com/spf13/cobra"
)

func newSendCmd() *cobra.Command {
	var filePath string
	var wait string
	var force bool

	cmd := &cobra.Command{
		Use:   "send <id> [message]",
		Short: "Send a text message to a running worker's herdr pane",
		Args: usageArgs(func(c *cobra.Command, args []string) error {
			want := 2
			if filePath != "" {
				want = 1
			}
			return cobra.ExactArgs(want)(c, args)
		}),
		RunE: func(cmd *cobra.Command, args []string) error {
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			message := ""
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
				wait = configDefault(fleetHome, "send-wait", "2m")
			}
			waitDuration, err := time.ParseDuration(wait)
			if err != nil {
				return usageValue(waitFromFlag, fmt.Errorf("invalid wait duration %q: %w", wait, err))
			}

			history, err := state.ReadHistoryReadOnly(fleetHome, args[0])
			if err != nil {
				return asPrecondition(err)
			}
			var attempt *state.Attempt
			if history.ActiveAttempt != nil {
				attempt = history.ActiveAttempt
			}
			client := herdrClientForAttempt(attempt, nil)
			result, err := steering.Execute(steering.Request{
				Home: fleetHome, TaskID: args[0], Message: message, Origin: steeringOperator,
				Client: client, Wait: waitDuration, WaitComposer: client.WaitComposerEmpty, TryTaskLock: true, Force: force,
			})
			if err != nil {
				return wrapSendError(err)
			}

			var doc axi.Doc
			doc.Field("id", args[0])
			doc.Field("result", "sent")
			doc.Field("send_id", strconv.FormatInt(result.Send.ID, 10))
			doc.Field("attempt", strconv.FormatInt(result.Send.AttemptID, 10))
			doc.Field("send_state", string(result.Send.State))
			doc.Int("chars", len([]rune(message)))
			submitKey := "Enter"
			if strings.Contains(result.Send.ReasonCode, "tab-queued") {
				submitKey = "Tab"
			}
			doc.Help("The terminal pane accepted the message and " + submitKey + "; run `hand status " + args[0] + "` to read what the worker does with it")
			return doc.Render(cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&filePath, "file", "", "read the message from this file instead of the positional argument")
	cmd.Flags().StringVar(&wait, "wait", "", "how long to wait for a busy composer to free before giving up (default: config/send-wait, or 2m)")
	cmd.Flags().BoolVar(&force, "force", false, "send without waiting for a busy composer; still records the send and verifies pane ownership")
	return cmd
}

const steeringOperator = "operator"

func wrapSendError(err error) error {
	var sendErr *steering.Error
	if !errors.As(err, &sendErr) {
		return err
	}
	code := 1
	switch {
	case sendErr.Precondition:
		code = 3
	case sendErr.State == state.SendNotSubmitted:
		code = 6
	case sendErr.State == state.SendUncertain || sendErr.State == state.SendPending:
		code = 7
	}
	if code == 1 {
		return err
	}
	return &ExitError{Err: err, Code: code}
}
