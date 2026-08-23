package supervision

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/atqamz/hand/internal/orientation"
	"github.com/atqamz/hand/internal/watcher"
)

// Overridable seams onto the existing level-triggered watcher boundary.
var (
	acquireWatcherOwnership = watcher.AcquireContext
	runWatcherUntilEvent    = watcher.RunUntilEvent
	watcherAttached         = watcher.IsAttached
)

// WaitConfig carries the host name and everything the watcher boundary needs
// to arm and poll. Timeout bounds the whole wait and only ever produces the
// explicit checkpoint result (exit 4) the caller asked for.
type WaitConfig struct {
	Host           string
	PollInterval   time.Duration
	StaleThreshold time.Duration
	ParkedBounds   watcher.ParkedBounds
	Timeout        time.Duration
}

// Wake is one coalesced delivery: every currently eligible episode collapses
// into one reasoning opportunity rather than one turn per subject.
type Wake struct {
	Host     string
	FleetID  string
	Text     string
	Episodes []Episode
}

// EvidenceReader returns the unbounded underlying actionable evidence for the
// resolved Fleet. It is the wake-eligibility source; rendered bounded
// orientation is never consulted here.
type EvidenceReader func(context.Context) (orientation.Evidence, error)

// Waiter bundles what a single wait invocation reads against.
type Waiter struct {
	Home         string
	ReadEvidence EvidenceReader
	Ledger       *Ledger
}

// Wait blocks until at least one current actionable episode is wake-eligible,
// records the delivery request, and returns the coalesced wake. Typed watcher
// results pass through for the caller's exit-taxonomy mapping.
func Wait(ctx context.Context, w Waiter, cfg WaitConfig) (Wake, error) {
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadlineCause(ctx, time.Now().Add(cfg.Timeout),
			fmt.Errorf("no eligible wake within %s: %w", cfg.Timeout, watcher.ErrNoEvent))
		defer cancel()
	}

	wake, _, eligible, err := currentEpisodes(ctx, w, cfg.Host)
	if err != nil || len(eligible) > 0 {
		if len(eligible) > 0 {
			return requestDelivery(w, wake, eligible)
		}
		return Wake{}, err
	}

	attached, err := watcherAttached(w.Home)
	if err != nil {
		return Wake{}, err
	}
	if attached {
		return pollUntilEligible(ctx, w, cfg)
	}
	return waitOwned(ctx, w, cfg)
}

func requestDelivery(w Waiter, wake Wake, eligible []Episode) (Wake, error) {
	if err := w.Ledger.MarkRequested(eligible); err != nil {
		return Wake{}, err
	}
	wake.Episodes = eligible
	wake.Text = WakeText(wake.FleetID)
	return wake, nil
}

// Derives episodes from authoritative evidence and reports all of them plus
// the subset that may wake now. An already-actionable condition has no next
// transition left to wait for, so arming observes it first.
func currentEpisodes(ctx context.Context, w Waiter, host string) (Wake, []Episode, []Episode, error) {
	evidence, err := w.ReadEvidence(ctx)
	if err != nil {
		return Wake{}, nil, nil, err
	}
	episodes := FromEvidence(evidence)
	wake := Wake{Host: host, FleetID: evidence.FleetID}
	return wake, episodes, w.Ledger.Eligible(episodes), nil
}

// Serves the case where another watcher owns the fleet home: stealing its
// ownership would break that arm, so this wait levels on authoritative
// evidence instead of watcher edges.
func pollUntilEligible(ctx context.Context, w Waiter, cfg WaitConfig) (Wake, error) {
	for {
		if err := sleepCtx(ctx, intervalOr(cfg.PollInterval)); err != nil {
			return Wake{}, err
		}
		wake, _, eligible, err := currentEpisodes(ctx, w, cfg.Host)
		if err != nil {
			return Wake{}, err
		}
		if len(eligible) > 0 {
			return requestDelivery(w, wake, eligible)
		}
	}
}

// Holds watcher ownership and drives RunUntilEvent cycles until a delivered
// cycle leaves something eligible. All-deduped cycles must not spin: an
// unchanged key set backs off one poll interval; new episodes retry at once.
func waitOwned(ctx context.Context, w Waiter, cfg WaitConfig) (Wake, error) {
	ownership, err := acquireWatcherOwnership(ctx, w.Home, false)
	if err != nil {
		return Wake{}, err
	}
	defer ownership.Release()

	var previous []string
	for {
		var caught bytes.Buffer
		err := runWatcherUntilEvent(ctx, ownedWatcherConfig(w, cfg), &caught, io.Discard)
		if err != nil {
			return Wake{}, err
		}
		wake, episodes, eligible, catchUpErr := currentEpisodes(ctx, w, cfg.Host)
		if catchUpErr != nil {
			return Wake{}, catchUpErr
		}
		if len(eligible) > 0 {
			return requestDelivery(w, wake, eligible)
		}
		keys := Keys(episodes)
		if slices.Equal(keys, previous) {
			if err := sleepCtx(ctx, intervalOr(cfg.PollInterval)); err != nil {
				return Wake{}, err
			}
		}
		previous = keys
	}
}

func ownedWatcherConfig(w Waiter, cfg WaitConfig) watcher.Config {
	return watcher.Config{
		Home:           w.Home,
		PollInterval:   intervalOr(cfg.PollInterval),
		StaleThreshold: cfg.StaleThreshold,
		Timeout:        remainingOf(cfg.Timeout),
		ParkedBounds:   cfg.ParkedBounds,
	}
}

func remainingOf(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 0
	}
	return timeout
}

func intervalOr(d time.Duration) time.Duration {
	if d <= 0 {
		return 5 * time.Second
	}
	return d
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}
