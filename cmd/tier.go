package cmd

import (
	"fmt"

	"github.com/atqamz/hand/internal/runtime"
	"github.com/spf13/cobra"
)

func resolveTier(cmd *cobra.Command, home, briefAbs, harnessName, model, effort string) (string, string, bool, error) {
	result, err := runtime.ResolveTier(home, briefAbs, harnessName, model, effort)
	if err != nil {
		return "", "", false, err
	}
	for _, warning := range result.Warnings {
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), warning); err != nil {
			return "", "", false, err
		}
	}
	return result.Model, result.Effort, result.BriefHasFrontMatter, nil
}
