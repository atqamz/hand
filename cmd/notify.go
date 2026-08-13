package cmd

import (
	"errors"
	"fmt"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/notify"
	"github.com/spf13/cobra"
)

func newNotifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notify <message>",
		Short: "Send an out-of-band notification",
		Long: "config/notify contains POSIX shell source, executed through sh -c on Linux, macOS, and Windows.\n" +
			"The notification text is available as $HAND_MESSAGE. On Windows, a POSIX-compatible sh must be directly available on PATH;\n" +
			"Hand does not reinterpret the template as cmd.exe, PowerShell, or WSL syntax, invoke wsl.exe, or rewrite paths.\n" +
			"Literal Windows paths remain POSIX shell source and need POSIX-compatible quoting or escaping.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			message := args[0]
			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			// Absence and send failure both mean nothing reached the only channel,
			// so neither may retain the old exit-0 delivery claim.
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
