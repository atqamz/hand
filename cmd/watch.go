package cmd

import (
	"errors"
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
	var untilEvent bool
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Poll herdr agent states and report actionable events",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}

			if cmd.Flags().Changed("timeout") {
				if !untilEvent {
					return &ExitError{Err: errors.New("--timeout requires --until-event: the streaming watcher has no completion to bound"), Code: 2}
				}
				if timeout <= 0 {
					return &ExitError{Err: fmt.Errorf("invalid timeout %s: must be positive", timeout), Code: 2}
				}
			}

			pollFromFlag := poll != ""
			if !pollFromFlag {
				poll = configDefault(home, "watch-interval", "5s")
			}
			pollInterval, err := time.ParseDuration(poll)
			if err != nil {
				return usageValue(pollFromFlag, fmt.Errorf("invalid poll interval %q: %w", poll, err))
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

			cfg := watcher.Config{
				Home:           home,
				PollInterval:   pollInterval,
				StaleThreshold: staleThreshold,
				Timeout:        timeout,
			}
			if !untilEvent {
				return watcher.Run(ctx, cfg, cmd.OutOrStdout(), cmd.ErrOrStderr())
			}

			// The exit code is the delivery, so a watcher that stopped without an
			// event must not exit 0: a caller re-arming on 0 would read it as fleet
			// news, and one distinguishing it from a crash needs its own code.
			if err := watcher.RunUntilEvent(ctx, cfg, cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
				if errors.Is(err, watcher.ErrNoEvent) {
					return &ExitError{Err: err, Code: 4}
				}
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&poll, "poll", "", "poll interval (default: config/watch-interval, or 5s)")
	cmd.Flags().BoolVar(&untilEvent, "until-event", false, "block until the first event, print it, and exit 0")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "with --until-event, give up after this long and exit 4 (default: no timeout)")
	return cmd
}
