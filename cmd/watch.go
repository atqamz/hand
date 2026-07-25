package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/atqamz/secondhand/internal/watcher"
	"github.com/spf13/cobra"
)

const defaultStaleThreshold = 300 * time.Second

func newWatchCmd() *cobra.Command {
	var poll string

	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Poll herdr agent states and report actionable events",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}

			if poll == "" {
				poll = configDefault(home, "watch-interval", "5s")
			}
			pollInterval, err := time.ParseDuration(poll)
			if err != nil {
				return fmt.Errorf("invalid poll interval %q: %w", poll, err)
			}

			staleThreshold := defaultStaleThreshold
			if raw := configDefault(home, "stale-threshold", ""); raw != "" {
				seconds, err := strconv.Atoi(raw)
				if err != nil {
					return fmt.Errorf("invalid config/stale-threshold %q: %w", raw, err)
				}
				staleThreshold = time.Duration(seconds) * time.Second
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			return watcher.Run(ctx, watcher.Config{
				Home:           home,
				PollInterval:   pollInterval,
				StaleThreshold: staleThreshold,
			}, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&poll, "poll", "", "poll interval (default: config/watch-interval, or 5s)")
	return cmd
}
