package cmd

import (
	"errors"
	"strings"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/registry"
	"github.com/spf13/cobra"
)

func newFleetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fleet",
		Short: "List known Fleet homes for this user",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			registryPath, err := registry.Path()
			if err != nil {
				return err
			}
			currentHome, currentErr := home.Resolve()
			var fleets []registry.Fleet
			registryDB, err := registry.OpenReadOnlyAt(registryPath)
			switch {
			case errors.Is(err, registry.ErrRegistryMissing):
			case err != nil:
				return err
			default:
				defer func() { _ = registryDB.Close() }()
				fleets, err = registryDB.List(currentHome)
				if err != nil {
					return err
				}
			}

			doc := axi.Doc{}
			doc.Field("registry", registryPath)
			doc.Field("current_home", orNone(currentHome))
			if currentErr != nil {
				doc.Field("current_error", currentErr.Error())
			} else {
				doc.Field("current_error", "none")
			}
			rows := make([][]string, 0, len(fleets))
			for _, fleet := range fleets {
				rows = append(rows, []string{
					fleet.ID,
					fleet.Home,
					string(fleet.State),
					boolString(fleet.Current),
					strings.Join(fleet.Locations, "\n"),
				})
			}
			doc.Rows("fleets", []string{"id", "home", "state", "current", "locations"}, rows)
			return doc.Render(cmd.OutOrStdout())
		},
	}
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
