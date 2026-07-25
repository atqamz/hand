package cmd

import (
	"fmt"

	"github.com/atqamz/secondhand/internal/selfupdate"
	"github.com/spf13/cobra"
)

func newUpdateCmd(version string) *cobra.Command {
	var checkOnly bool

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Self-update the hand binary from GitHub Releases",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			latest, err := selfupdate.LatestTag(selfupdate.Repo)
			if err != nil {
				return err
			}
			newer, err := selfupdate.IsNewer(latest, version)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if !newer {
				_, err := fmt.Fprintf(out, "hand %s is up to date\n", version)
				return err
			}

			if checkOnly {
				_, err := fmt.Fprintf(out, "update available: %s -> %s\n", version, latest)
				return err
			}

			if err := selfupdate.Apply(selfupdate.Repo, latest); err != nil {
				return err
			}
			_, err = fmt.Fprintf(out, "updated hand %s -> %s\n", version, latest)
			return err
		},
	}

	cmd.Flags().BoolVar(&checkOnly, "check", false, "check whether an update is available without installing")
	return cmd
}
