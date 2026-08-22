package cmd

import (
	"fmt"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/toolchain"
	"github.com/spf13/cobra"
)

func newRuntimeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Inspect and repair the private core runtime",
		Args:  usageArgs(cobra.NoArgs),
	}
	cmd.AddCommand(newRuntimeStatusCmd(), newRuntimeEnsureCmd())
	return cmd
}

func newRuntimeStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report the selected private runtime without changing it",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := toolchain.DefaultStore()
			if err != nil {
				return err
			}
			status, err := store.Status("", "")
			if err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("target", status.Target)
			doc.Field("runtime_id", status.RuntimeID)
			doc.Bool("ready", status.Ready)
			doc.Field("bundle", valueOrNone(status.BundleDir))
			doc.Field("git", valueOrNone(status.GitPath))
			doc.Field("git_version", valueOrNone(status.GitVersion))
			doc.Field("treehouse", valueOrNone(status.TreehousePath))
			doc.Field("treehouse_version", valueOrNone(status.TreehouseVersion))
			doc.Field("herdr", valueOrNone(status.HerdrPath))
			doc.Field("herdr_version", valueOrNone(status.HerdrVersion))
			doc.Field("reason", valueOrNone(status.Reason))
			doc.Help("Run `hand runtime ensure` to install or repair the exact private Git, Treehouse, and Herdr bundle")
			return doc.Render(cmd.OutOrStdout())
		},
	}
}

func newRuntimeEnsureCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ensure",
		Short: "Install or repair the exact private core runtime",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := toolchain.DefaultStore()
			if err != nil {
				return err
			}
			runtime, err := store.Ensure(cmd.Context(), "", "")
			if err != nil {
				return fmt.Errorf("ensure private Secondhand runtime: %w; run `hand runtime status` for diagnostics", err)
			}
			var doc axi.Doc
			doc.Field("result", "ready")
			doc.Field("runtime_id", runtime.ID)
			doc.Field("target", runtime.Target)
			doc.Field("bundle", runtime.BundleDir)
			doc.Field("git", runtime.GitPath)
			doc.Field("git_version", runtime.GitVersion)
			doc.Field("treehouse", runtime.TreehousePath)
			doc.Field("treehouse_version", runtime.TreehouseVersion)
			doc.Field("herdr", runtime.HerdrPath)
			doc.Field("herdr_version", runtime.HerdrVersion)
			return doc.Render(cmd.OutOrStdout())
		},
	}
}

func valueOrNone(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

func renderRuntimeWarnings(cmd *cobra.Command, warnings []string) error {
	for _, warning := range warnings {
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), warning); err != nil {
			return err
		}
	}
	return nil
}
