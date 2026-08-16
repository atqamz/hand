package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/watcher"
	"github.com/spf13/cobra"
)

const defaultStaleThreshold = 300 * time.Second
const defaultParkedPausedBound = 3600 * time.Second
const defaultParkedDoneBound = 5400 * time.Second
const defaultParkedOtherBound = 1200 * time.Second

var (
	acquireWatcher    = watcher.Acquire
	notifyWatchSignal = signal.Notify
	stopWatchSignal   = signal.Stop
)

func newWatchCmd() *cobra.Command {
	var poll string
	var untilEvent bool
	var timeout time.Duration
	var events []string
	var takeover bool

	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Poll herdr agent states and report actionable events",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			if cmd.Flags().Changed("timeout") {
				if !untilEvent {
					return &ExitError{Err: errors.New("--timeout requires --until-event: the streaming watcher has no completion to bound"), Code: 2}
				}
				if timeout <= 0 {
					return &ExitError{Err: fmt.Errorf("invalid timeout %s: must be positive", timeout), Code: 2}
				}
			}

			if cmd.Flags().Changed("event") {
				if !untilEvent {
					return &ExitError{Err: errors.New("--event requires --until-event: the streaming watcher has no wake to filter"), Code: 2}
				}
				known := make(map[string]bool, len(watcher.KnownKinds()))
				for _, k := range watcher.KnownKinds() {
					known[k] = true
				}
				for _, e := range events {
					if !known[e] {
						return &ExitError{Err: fmt.Errorf("unknown --event %q: want one of %s", e, strings.Join(watcher.KnownKinds(), ", ")), Code: 2}
					}
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

			staleThreshold, err := configSeconds(home, "stale-threshold", defaultStaleThreshold)
			if err != nil {
				return err
			}
			parkedPausedBound, err := configSeconds(home, "parked-paused-bound", defaultParkedPausedBound)
			if err != nil {
				return err
			}
			parkedDoneBound, err := configSeconds(home, "parked-done-bound", defaultParkedDoneBound)
			if err != nil {
				return err
			}
			parkedOtherBound, err := configSeconds(home, "parked-other-bound", defaultParkedOtherBound)
			if err != nil {
				return err
			}

			sig := make(chan os.Signal, 1)
			notifyWatchSignal(sig, os.Interrupt, syscall.SIGTERM)
			defer stopWatchSignal(sig)

			ownership, err := acquireWatcher(home, takeover)
			if err != nil {
				if errors.Is(err, watcher.ErrAttached) {
					return &ExitError{Err: err, Code: 3}
				}
				return err
			}
			defer ownership.Release()

			ctx, cancel := watchContext(cmd.Context(), sig, ownership.TakeoverRequested())
			defer cancel()

			cfg := watcher.Config{
				Home:           home,
				PollInterval:   pollInterval,
				StaleThreshold: staleThreshold,
				Timeout:        timeout,
				ParkedBounds: watcher.ParkedBounds{
					Paused: parkedPausedBound,
					Done:   parkedDoneBound,
					Other:  parkedOtherBound,
				},
				EventFilter: watcher.NewEventFilter(events),
			}
			if !untilEvent {
				return mapWatchResult(watcher.Run(ctx, cfg, cmd.OutOrStdout(), cmd.ErrOrStderr()))
			}

			// The exit code is the delivery, so a watcher that stopped without an
			// event must not exit 0: a caller re-arming on 0 would read it as fleet
			// news, and one distinguishing it from a crash needs its own code.
			return mapWatchResult(watcher.RunUntilEvent(ctx, cfg, cmd.OutOrStdout(), cmd.ErrOrStderr()))
		},
	}

	cmd.Flags().StringVar(&poll, "poll", "", "poll interval (default: config/watch-interval, or 5s)")
	cmd.Flags().BoolVar(&untilEvent, "until-event", false, "block until the first event, print it, and exit 0")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "with --until-event, give up after this long and exit 4 (default: no timeout)")
	cmd.Flags().StringSliceVar(&events, "event", nil, "with --until-event, wake only on these event kinds (default: any); repeatable or comma-separated")
	cmd.Flags().BoolVar(&takeover, "takeover", false, "request the current watcher to stop, then wait to acquire ownership")
	return cmd
}

// Converts a watcher termination error into the application exit taxonomy,
// resolved through errors.Is so wrapping preserves classification.
func mapWatchResult(err error) error {
	switch {
	case errors.Is(err, watcher.ErrReplaced):
		return &ExitError{Err: err, Code: 9}
	case errors.Is(err, watcher.ErrInterrupted):
		return &ExitError{Err: err, Code: 8}
	case errors.Is(err, watcher.ErrNoEvent):
		return &ExitError{Err: err, Code: 4}
	case errors.Is(err, watcher.ErrArmFailed):
		return &ExitError{Err: err, Code: 5}
	}
	return err
}
