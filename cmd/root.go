package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/atqamz/secondhand/internal/home"
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
			if home, err := home.Resolve(); err == nil {
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
	root.AddCommand(newHoldCmd())
	root.AddCommand(newTeardownCmd())
	root.AddCommand(newWatchCmd())
	root.AddCommand(newMergeCmd())
	root.AddCommand(newPRCmd())
	root.AddCommand(newPromoteCmd())
	root.AddCommand(newNotifyCmd())
	root.AddCommand(newSearchCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newUpdateCmd(version))
	// ExecuteC would add the completion group later, too late for the guard below.
	root.InitDefaultCompletionCmd()
	guardSubcommandGroups(root)
	return root
}

// guardSubcommandGroups makes every subcommand-only group below c reject an
// unknown subcommand with exit code 2. A group with no RunE trips cobra's
// Runnable() check, which short-circuits to a help dump and a zero exit before
// the group's Args validator ever runs, so the group needs both. Root is left
// alone: cobra's Find() already reports its unknown commands, and giving it a
// non-nil Args would suppress that.
func guardSubcommandGroups(c *cobra.Command) {
	for _, sub := range c.Commands() {
		if sub.HasSubCommands() && !sub.Runnable() {
			sub.Args = usageArgs(cobra.NoArgs)
			sub.RunE = func(cmd *cobra.Command, args []string) error { return cmd.Help() }
		}
		guardSubcommandGroups(sub)
	}
}

func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(c *cobra.Command, args []string) error {
		if err := validate(c, args); err != nil {
			return &ExitError{Err: err, Code: 2}
		}
		return nil
	}
}

// usageValue tags a rejected input value as exit code 2 only when it came from
// the command line. The same value read from a config/ default is a general
// error (code 1): nothing the invocation said was wrong.
func usageValue(fromFlag bool, err error) error {
	if fromFlag {
		return &ExitError{Err: err, Code: 2}
	}
	return err
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

// ExitError carries a non-default exit code that SPECS.md requires: 2 for a
// usage error (bad arg count, unknown flag, unknown subcommand, invalid
// argument or flag value) and 3 for a precondition failure like red CI or
// uncommitted changes, both distinct from the general-error code (1) cobra
// otherwise produces for any RunE error.
type ExitError struct {
	Err  error
	Code int
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }
