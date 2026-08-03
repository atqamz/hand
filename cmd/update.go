package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/atqamz/secondhand/internal/agentsmd"
	"github.com/atqamz/secondhand/internal/home"
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

			// The binary is already replaced by this point, so a failed
			// AGENTS.md refresh or skeleton seed is reported as a warning
			// rather than an error: exiting nonzero here reads as "the update
			// failed" and invites a pointless re-run.
			var refreshed bool
			var seedErr error
			fleetHome, refreshErr := home.Resolve()
			switch {
			case refreshErr == nil:
				refreshed, refreshErr = agentsmd.Refresh(fleetHome)
				// The refreshed template directs the agent at data files an
				// older home never had, so the command that installs it also
				// leaves those files in place - directories included, since a
				// home resolves as one on its state/hand.db marker alone.
				seedErr = initLayout(fleetHome)
			case errors.Is(refreshErr, home.ErrNotFound):
				refreshErr = nil
			}

			notes, _ := selfupdate.ReleaseNotes(selfupdate.Repo, latest)
			notes = strings.TrimSpace(notes)

			if _, err := fmt.Fprintf(out, "current: %s\n", version); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "latest:  %s\n", latest); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "updated hand to %s\n", latest); err != nil {
				return err
			}
			if refreshed {
				if _, err := fmt.Fprintln(out, "updated AGENTS.md template"); err != nil {
					return err
				}
			}
			if refreshErr != nil {
				if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "warning: refresh AGENTS.md: %v\n", refreshErr); err != nil {
					return err
				}
			}
			if seedErr != nil {
				if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "warning: seed data skeletons: %v\n", seedErr); err != nil {
					return err
				}
			}
			if notes != "" {
				if _, err := fmt.Fprintf(out, "changed:\n%s\n", notes); err != nil {
					return err
				}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&checkOnly, "check", false, "check whether an update is available without installing")
	return cmd
}
