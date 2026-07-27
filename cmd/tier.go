package cmd

import (
	"fmt"

	"github.com/atqamz/secondhand/internal/brief"
	"github.com/atqamz/secondhand/internal/harness"
	"github.com/spf13/cobra"
)

func resolveTier(cmd *cobra.Command, home, briefAbs, harnessName, model, effort string) (resolvedModel, resolvedEffort string, frontMatter bool, err error) {
	decl, frontMatter, err := brief.Parse(briefAbs)
	if err != nil {
		return "", "", false, fmt.Errorf("parse brief %s: %w", briefAbs, err)
	}

	resolvedModel = model
	if resolvedModel == "" {
		resolvedModel = decl.Model
	}
	if resolvedModel == "" {
		resolvedModel = configDefault(home, "model", "")
	}

	resolvedEffort = effort
	if resolvedEffort == "" {
		resolvedEffort = decl.Effort
	}
	if resolvedEffort == "" {
		resolvedEffort = configDefault(home, "effort", "")
	}

	if resolvedEffort != "" && !harness.SupportsEffort(harnessName) {
		if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "warning: harness %q has no effort flag, ignoring effort %q\n", harnessName, resolvedEffort); err != nil {
			return "", "", false, err
		}
	}

	return resolvedModel, resolvedEffort, frontMatter, nil
}
