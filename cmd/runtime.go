package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func renderRuntimeWarnings(cmd *cobra.Command, warnings []string) error {
	for _, warning := range warnings {
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), warning); err != nil {
			return err
		}
	}
	return nil
}
