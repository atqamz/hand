package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:     "hand",
		Short:   "Talk to one agent. Ship with a crew.",
		Version: version,
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
