package cmd

import (
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
	return root
}

func Execute(version string) {
	if err := newRootCmd(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
