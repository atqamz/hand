package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/atqamz/secondhand/internal/selfupdate"
	"github.com/spf13/cobra"
)

func newRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:     "hand",
		Short:   "Talk to one agent. Ship with a crew.",
		Version: version,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Name() == "update" {
				return nil
			}
			if home, err := os.Getwd(); err == nil {
				if notice := selfupdate.CheckNotice(home, selfupdate.Repo, version); notice != "" {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), notice)
				}
			}
			return nil
		},
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.AddCommand(newInitCmd())
	root.AddCommand(newProjectCmd())
	root.AddCommand(newSpawnCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newSendCmd())
	root.AddCommand(newTeardownCmd())
	root.AddCommand(newWatchCmd())
	root.AddCommand(newMergeCmd())
	root.AddCommand(newPromoteCmd())
	root.AddCommand(newNotifyCmd())
	root.AddCommand(newUpdateCmd(version))
	return root
}

func Execute(version string) {
	if err := newRootCmd(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		code := 1
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.Code
		}
		os.Exit(code)
	}
}

// ExitError carries the precondition-failure exit code (3) that SPECS.md
// requires for refusals like red CI or uncommitted changes, distinct from
// the general-error code (1) cobra otherwise produces for any RunE error.
type ExitError struct {
	Err  error
	Code int
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }
