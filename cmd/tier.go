package cmd

import (
	"fmt"

	"github.com/atqamz/secondhand/internal/brief"
	"github.com/atqamz/secondhand/internal/harness"
	"github.com/spf13/cobra"
)

// resolveTier applies the model/effort precedence spawn and promote share: an explicit flag
// wins, then the brief's own "---" front matter declaration, then config/model and
// config/effort, then the harness default. It is the one place that chain lives, since spawn
// and promote are already near-identical here. frontMatter reports whether briefAbs carries a
// declaration block at all, so the caller can pass it to harness.Build and keep it out of the
// worker's prompt as an instruction. A resolved effort the chosen harness cannot apply is
// reported to stderr, not dropped silently.
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
