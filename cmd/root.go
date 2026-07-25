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
		// Cobra's own error/usage printing is disabled; Execute prints exactly
		// one line to stderr and picks the exit code, so nothing else should.
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return &ExitError{Err: err, Code: 2}
	})
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

// usageArgs wraps a cobra.PositionalArgs validator so a mismatch is tagged as
// exit code 2 (usage error) rather than falling through to the general code 1.
func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(c *cobra.Command, args []string) error {
		if err := validate(c, args); err != nil {
			return &ExitError{Err: err, Code: 2}
		}
		return nil
	}
}

func Execute(version string) {
	root := newRootCmd(version)
	found, err := root.ExecuteC()
	if err == nil {
		return
	}

	fmt.Fprintln(os.Stderr, err)
	code := 1
	var exitErr *ExitError
	switch {
	case errors.As(err, &exitErr):
		code = exitErr.Code
	case found == root:
		// cobra's own dispatch failed before reaching any subcommand's Args
		// check (e.g. an unknown command name) - untagged, but still a usage error.
		code = 2
	}
	os.Exit(code)
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
