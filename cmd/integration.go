package cmd

import (
	"fmt"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/integration"
	"github.com/spf13/cobra"
)

var integrationFields = []axi.Column[integration.Status]{
	{Name: "id", Value: func(status integration.Status) string { return status.Capability.ID }},
	{Name: "executable", Value: func(status integration.Status) string { return status.Capability.Executable }},
	{Name: "owner", Value: func(status integration.Status) string { return status.Capability.Owner }},
	{Name: "state", Value: func(status integration.Status) string { return string(status.State) }},
	{Name: "path", Value: func(status integration.Status) string { return valueOrNone(status.Path) }},
}

func newIntegrationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "integration",
		Short: "Inspect and explicitly manage optional first-party capabilities",
		Args:  usageArgs(cobra.NoArgs),
	}
	cmd.AddCommand(newIntegrationListCmd(), newIntegrationInstallCmd(), newIntegrationRemoveCmd())
	return cmd
}

func newIntegrationListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List optional capabilities without installing them",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			statuses, err := integration.DefaultStore().List()
			if err != nil {
				return err
			}
			var doc axi.Doc
			axi.Table(&doc, "capabilities", statuses, integrationFields)
			doc.Help("Optional capabilities are not downloaded by core bootstrap; install only the capability required by an operation")
			return doc.Render(cmd.OutOrStdout())
		},
	}
}

func newIntegrationInstallCmd() *cobra.Command {
	var path string

	cmd := &cobra.Command{
		Use:   "install <id>",
		Short: "Install one closed first-party capability explicitly",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if path == "" {
				return fmt.Errorf("optional capability %q has no qualified download in this Hand build; pass --path to an explicitly installed executable", args[0])
			}
			installed, err := integration.DefaultStore().Install(args[0], path)
			if err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("result", "installed")
			doc.Field("id", args[0])
			doc.Field("path", installed)
			doc.Help("The executable was copied into the private integration store; the parent PATH was unchanged")
			return doc.Render(cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "absolute path to an already installed capability executable")
	return cmd
}

func newIntegrationRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <id>",
		Short: "Remove one selected optional capability",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := integration.DefaultStore().Remove(args[0]); err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("result", "removed")
			doc.Field("id", args[0])
			doc.Help("The selected payload was unlinked; versioned payload files remain available for recovery")
			return doc.Render(cmd.OutOrStdout())
		},
	}
}
