package cmd

import (
	"context"
	"errors"
	"strings"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/registry"
	"github.com/atqamz/hand/internal/state"
	"github.com/spf13/cobra"
)

func newFleetCmd() *cobra.Command {
	cmd := &cobra.Command{
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
	cmd.AddCommand(newFleetHerdrCmd())
	cmd.AddCommand(newFleetPruneCmd())
	return cmd
}

// Opt-in only, and only ever by classification: bare `hand fleet prune` reports the Fleets it would
// drop and drops nothing; --apply removes exactly those. Never a side effect of any other command,
// and never anything short of the current classification (see registry.MissingFleets).
func newFleetPruneCmd() *cobra.Command {
	var apply bool
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Report or remove registered Fleets classified missing",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			registryPath, err := registry.Path()
			if err != nil {
				return err
			}
			currentHome, _ := home.Resolve()

			var fleets []registry.Fleet
			if apply {
				registryDB, err := registry.OpenAt(registryPath)
				if err != nil {
					return err
				}
				defer func() { _ = registryDB.Close() }()
				fleets, err = registryDB.Prune(currentHome)
				if err != nil {
					return err
				}
			} else {
				registryDB, err := registry.OpenReadOnlyAt(registryPath)
				switch {
				case errors.Is(err, registry.ErrRegistryMissing):
				case err != nil:
					return err
				default:
					defer func() { _ = registryDB.Close() }()
					fleets, err = registryDB.MissingFleets(currentHome)
					if err != nil {
						return err
					}
				}
			}

			doc := axi.Doc{}
			doc.Field("registry", registryPath)
			doc.Bool("applied", apply)
			label := "candidates"
			if apply {
				label = "removed"
			}
			rows := make([][]string, 0, len(fleets))
			for _, fleet := range fleets {
				rows = append(rows, []string{fleet.ID, fleet.Home, string(fleet.State), strings.Join(fleet.Locations, "\n")})
			}
			doc.Rows(label, []string{"id", "home", "state", "locations"}, rows)
			if apply {
				doc.Help("Run `hand fleet` to confirm the registry now holds only what these Fleets left behind")
			} else {
				doc.Help("Run `hand fleet prune --apply` to remove exactly these Fleets; nothing was changed")
			}
			return doc.Render(cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "remove the reported Fleets instead of only reporting them")
	return cmd
}

type fleetHerdrCommand interface {
	Session() string
	Observe(context.Context) herdr.SessionObservation
	Ensure(context.Context) error
	Open(context.Context) error
}

var newFleetHerdrCommand = func(fleetID string) fleetHerdrCommand {
	return herdr.NewFleetHerdr(fleetID)
}

func newFleetHerdrCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "herdr",
		Short: "Ensure and attach the current Fleet Herdr session",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			fleetID, fleetHerdr, err := currentFleetHerdrCommand()
			if err != nil {
				return asPrecondition(err)
			}
			if err := fleetHerdr.Open(cmd.Context()); err != nil {
				return asPrecondition(err)
			}
			doc := axi.Doc{}
			doc.Field("fleet_id", fleetID)
			doc.Field("herdr_session", fleetHerdr.Session())
			doc.Bool("opened", true)
			return doc.Render(cmd.OutOrStdout())
		},
	}
	cmd.AddCommand(newFleetHerdrStatusCmd())
	return cmd
}

func newFleetHerdrStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Observe the current Fleet Herdr session",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			fleetID, fleetHerdr, err := currentFleetHerdrCommand()
			if err != nil {
				return asPrecondition(err)
			}
			observation := fleetHerdr.Observe(cmd.Context())
			doc := axi.Doc{}
			doc.Field("fleet_id", fleetID)
			doc.Field("herdr_session", fleetHerdr.Session())
			doc.Field("state", string(observation.State))
			doc.Field("reason", orNone(observation.Reason))
			return doc.Render(cmd.OutOrStdout())
		},
	}
}

func currentFleetHerdrCommand() (string, fleetHerdrCommand, error) {
	fleetHome, err := home.Resolve()
	if err != nil {
		return "", nil, err
	}
	fleetID, err := state.FleetIDReadOnly(fleetHome)
	if err != nil {
		return "", nil, err
	}
	return fleetID, newFleetHerdrCommand(fleetID), nil
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
