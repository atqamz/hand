package cmd

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/atqamz/secondhand/internal/brief"
	"github.com/atqamz/secondhand/internal/harness"
	"github.com/spf13/cobra"
)

func resolveTier(cmd *cobra.Command, home, briefAbs, harnessName, model, effort string) (resolvedModel, resolvedEffort string, frontMatter bool, err error) {
	decl, frontMatter, err := brief.Parse(briefAbs)
	if err != nil {
		return "", "", false, fmt.Errorf("parse brief %s: %w", briefAbs, err)
	}

	resolvedModel = cmp.Or(model, decl.Model, workerDefault(home, settingModel, harnessName))
	resolvedEffort = cmp.Or(effort, decl.Effort, workerDefault(home, settingEffort, harnessName))

	var dropped []string
	if resolvedModel != "" && !harness.SupportsModel(harnessName) {
		dropped = append(dropped, fmt.Sprintf("model %q", resolvedModel))
	}
	if resolvedEffort != "" && !harness.SupportsEffort(harnessName) {
		dropped = append(dropped, fmt.Sprintf("effort %q", resolvedEffort))
	}
	if !harness.CarriesPrompt(harnessName) {
		dropped = append(dropped, "the operator-decision rule")
		if frontMatter {
			dropped = append(dropped, "the front-matter disclaimer")
		}
	}

	// One line per launch rather than one per dropped value: consecutive warnings all naming the
	// same harness read as separate problems (atqamz/secondhand#151).
	if len(dropped) > 0 {
		if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "warning: harness %q cannot carry %s; launching anyway\n", harnessName, strings.Join(dropped, ", ")); err != nil {
			return "", "", false, err
		}
	}

	return resolvedModel, resolvedEffort, frontMatter, nil
}
