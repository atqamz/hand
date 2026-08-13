package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/atqamz/hand/internal/agentsmd"
	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/selfupdate"
	"github.com/atqamz/hand/internal/sessionhook"
	"github.com/spf13/cobra"
)

func newUpdateCmd(version string) *cobra.Command {
	return newUpdateCmdWithBuildInfo(legacyBuildInfo(version))
}

func newUpdateCmdWithBuildInfo(info selfupdate.BuildInfo) *cobra.Command {
	var checkOnly bool
	var requestedChannel string

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Self-update the hand binary from GitHub Releases",
		Args:  usageArgs(cobra.NoArgs),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if requestedChannel != "" && requestedChannel != selfupdate.ChannelStable && requestedChannel != selfupdate.ChannelEdge {
				return usageValue(true, fmt.Errorf("invalid release channel %q", requestedChannel))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			targetChannel := info.Channel
			if targetChannel == selfupdate.ChannelDev {
				targetChannel = selfupdate.ChannelStable
			}
			if requestedChannel != "" {
				targetChannel = requestedChannel
			}

			target, err := selfupdate.ResolveTarget(selfupdate.Repo, targetChannel)
			if err != nil {
				return err
			}
			newer, err := selfupdate.NeedsUpdate(info, target)
			if err != nil {
				return err
			}

			if !newer || checkOnly {
				doc := updateDoc(info, target, newer, false, "not-applicable", "not-applicable", nil)
				if newer {
					doc.Help(updateHelp(target, requestedChannel != ""))
				}
				return doc.Render(cmd.OutOrStdout())
			}

			if err := selfupdate.Apply(selfupdate.Repo, target.Tag); err != nil {
				return err
			}

			// The binary is already replaced by this point, so a failed AGENTS.md refresh or skeleton seed is
			// reported as a warning rather than an error: exiting nonzero here reads as "the update failed" and
			// invites a pointless re-run.
			var refreshed, hookRemoved bool
			var seedErr, hookErr error
			fleetHome, refreshErr := home.Resolve()
			switch {
			case refreshErr == nil:
				refreshed, refreshErr = agentsmd.Refresh(fleetHome)
				// The refreshed template directs the agent at data files an older home never had, so the command
				// that installs it also leaves those files in place - directories included, since a home resolves
				// as one on its state/hand.db marker alone.
				seedErr = initLayout(fleetHome)
				// Generated instructions supersede the Claude-only hook, so an
				// update retires Secondhand's command without touching others.
				if refreshErr == nil {
					var exe string
					if exe, hookErr = os.Executable(); hookErr == nil {
						hookRemoved, hookErr = sessionhook.Remove(fleetHome, exe)
					}
				}
			case errors.Is(refreshErr, home.ErrNotFound):
				refreshErr = nil
			}

			if refreshErr != nil {
				if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "warning: refresh AGENTS.md: %v\n", refreshErr); err != nil {
					return err
				}
			}
			if hookErr != nil {
				if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "warning: retire the session hook: %v\n", hookErr); err != nil {
					return err
				}
			}
			if seedErr != nil {
				if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "warning: seed data skeletons: %v\n", seedErr); err != nil {
					return err
				}
			}
			notes, _ := selfupdate.ReleaseNotes(selfupdate.Repo, target.Tag)

			doc := updateDoc(
				info,
				target,
				true,
				true,
				refreshOutcome(fleetHome, refreshed, refreshErr),
				retirementOutcome(fleetHome, hookRemoved, hookErr),
				releaseNoteLines(notes),
			)
			doc.Help("Run `hand doctor` to check this home's AGENTS.md against the template " + target.Version + " installed")
			return doc.Render(cmd.OutOrStdout())
		},
	}

	cmd.Flags().BoolVar(&checkOnly, "check", false, "check whether an update is available without installing")
	cmd.Flags().StringVar(&requestedChannel, "channel", "", "release channel to target (stable or edge)")
	return cmd
}

func updateDoc(info selfupdate.BuildInfo, target selfupdate.Target, available, updated bool, agentsMD, sessionHook string, notes []string) axi.Doc {
	var doc axi.Doc
	doc.Field("current", info.Version)
	doc.Field("current_channel", info.Channel)
	doc.Field("current_commit", displayCommit(info.Commit))
	doc.Field("latest", target.Version)
	doc.Field("latest_channel", target.Channel)
	doc.Field("latest_commit", displayCommit(target.Commit))
	doc.Bool("update_available", available)
	doc.Bool("updated", updated)
	doc.Field("agents_md", agentsMD)
	doc.Field("session_hook", sessionHook)
	doc.List("notes", notes)
	return doc
}

func displayCommit(commit string) string {
	if commit == "" {
		return "unknown"
	}
	return commit
}

func updateHelp(target selfupdate.Target, explicit bool) string {
	command := "hand update"
	if explicit {
		command += " --channel " + target.Channel
	}
	return "Run `" + command + "` to install " + target.Version + ", which also refreshes this home's AGENTS.md template"
}

// The binary is replaced whatever this says, so every outcome is a value of
// one field rather than a line that appears or does not.
func refreshOutcome(fleetHome string, changed bool, err error) string {
	switch {
	case err != nil:
		return "failed"
	case fleetHome == "":
		return "no-fleet-home"
	case changed:
		return "refreshed"
	default:
		return "unchanged"
	}
}

func retirementOutcome(fleetHome string, changed bool, err error) string {
	switch {
	case err != nil:
		return "failed"
	case fleetHome == "":
		return "no-fleet-home"
	case changed:
		return "removed"
	default:
		return "unchanged"
	}
}

func releaseNoteLines(notes string) []string {
	var lines []string
	for _, line := range strings.Split(notes, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}
