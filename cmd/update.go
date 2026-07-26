package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/atqamz/secondhand/internal/agentsmd"
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

			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			refreshed, err := agentsmd.Refresh(cwd)
			if err != nil {
				return err
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
