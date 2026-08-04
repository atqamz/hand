package cmd

import (
	"fmt"
	"time"

	"github.com/atqamz/secondhand/internal/home"
	"github.com/atqamz/secondhand/internal/state"
	"github.com/spf13/cobra"
)

func newHoldCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hold",
		Short: "Set or clear a hold recording what an id is waiting on",
	}
	cmd.AddCommand(newHoldSetCmd())
	cmd.AddCommand(newHoldClearCmd())
	return cmd
}

func newHoldSetCmd() *cobra.Command {
	var kind, reason, blockedOn string

	cmd := &cobra.Command{
		Use:   "set <id>",
		Short: "Set a hold on an id, live task or not",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if err := validateHoldKind(kind); err != nil {
				return err
			}
			if reason == "" {
				return &ExitError{Err: fmt.Errorf("--reason is required"), Code: 2}
			}
			if kind == state.HoldKindBlocked && blockedOn == "" {
				return &ExitError{Err: fmt.Errorf("--blocked-on is required for a %s hold", state.HoldKindBlocked), Code: 2}
			}
			if kind != state.HoldKindBlocked && blockedOn != "" {
				return &ExitError{Err: fmt.Errorf("--blocked-on only applies to a %s hold", state.HoldKindBlocked), Code: 2}
			}

			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			h := state.Hold{
				ID: id, Kind: kind, Reason: reason, BlockedOn: blockedOn,
				SetAt: time.Now().UTC().Format(time.RFC3339),
			}
			if err := state.SetHold(home, h); err != nil {
				return asPrecondition(err)
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "hold set on %s (kind=%s)\n", id, kind); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&kind, "kind", "", "hold kind: operator or blocked")
	cmd.Flags().StringVar(&reason, "reason", "", "why the id is waiting")
	cmd.Flags().StringVar(&blockedOn, "blocked-on", "", "id being waited on (blocked holds only)")
	return cmd
}

// The limit kind is refused with its own message rather than falling through to
// the generic one: it is a real kind hand status renders and hand spawn honors,
// so an operator who names it deserves to be told who owns it instead of that it
// does not exist.
func validateHoldKind(kind string) error {
	switch kind {
	case state.HoldKindOperator, state.HoldKindBlocked:
		return nil
	case state.HoldKindLimit:
		return &ExitError{Err: fmt.Errorf("hold kind %s is set by hand watch when a worker's harness stops on a usage limit, and cleared when it runs again", state.HoldKindLimit), Code: 2}
	default:
		return &ExitError{Err: fmt.Errorf("invalid hold kind %q: must be %s or %s", kind, state.HoldKindOperator, state.HoldKindBlocked), Code: 2}
	}
}

func newHoldClearCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clear <id>",
		Short: "Clear the hold on an id",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			if err := state.ClearHold(home, id); err != nil {
				return asPrecondition(err)
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "hold cleared on %s\n", id); err != nil {
				return err
			}
			return nil
		},
	}
	return cmd
}
