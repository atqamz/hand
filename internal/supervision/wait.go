package supervision

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
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
	Host string
	// RuntimeSession names the exact host runtime arming this wait (an
	// OpenCode session ID, a Codex thread). It scopes the bridge-attachment
	// record so a secondary runtime defers instead of stealing.
	RuntimeSession string
	PollInterval   time.Duration
	StaleThreshold time.Duration
	ParkedBounds   watcher.ParkedBounds
	Timeout        time.Duration
}

// Wake is one coalesced delivery: every currently eligible episode collapses
// into one reasoning opportunity rather than one turn per subject.
type Wake struct {
	Schema   string `json:"schema"`
	FleetID  string `json:"fleet_id"`
	Host     string `json:"host"`
	Message  string `json:"message"`
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

// WakeSchema versions the machine protocol host adapters consume.
const WakeSchema = "hand.supervision.wake.v1"

// Wait blocks until at least one current actionable episode can be claimed
// atomically, records the claim plus this runtime's bridge attachment, and
// returns the coalesced wake. Typed watcher results pass through unchanged.
func Wait(ctx context.Context, w Waiter, cfg WaitConfig) (Wake, error) {
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadlineCause(ctx, time.Now().Add(cfg.Timeout),
			fmt.Errorf("no eligible wake within %s: %w", cfg.Timeout, watcher.ErrNoEvent))
		defer cancel()
	}

	wake, allEpisodes, eligible, err := currentEpisodes(ctx, w, cfg.Host)
	if err != nil {
		return Wake{}, err
	}
	if len(eligible) > 0 {
		wake, won, claimErr := claimAndDeliver(w, cfg, wake, allEpisodes)
		if claimErr != nil || won {
			return wake, claimErr
		}
		// Another waiter claimed these episodes first: fall through and keep
		// waiting instead of answering an empty success.
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

// Derives episodes from authoritative evidence and reports all of them plus
// the subset that may be claimed now. An already-actionable condition has no
// next transition left to wait for, so arming observes it first.
func currentEpisodes(ctx context.Context, w Waiter, host string) (Wake, []Episode, []Episode, error) {
	evidence, err := w.ReadEvidence(ctx)
	if err != nil {
		return Wake{}, nil, nil, err
	}
	episodes := FromEvidence(evidence)
	wake := Wake{Host: host, FleetID: evidence.FleetID}
	return wake, episodes, w.Ledger.Eligible(episodes), nil
}

// CAS-claims the host bridge under the attachment file lock, keeping its
// record fresh until stopped. Faults and theft arrive on the guard channel:
// a waiter running after losing its record would otherwise read as attached.
func acquireBridge(ctx context.Context, w Waiter, cfg WaitConfig, fleetID string) (*bridgeGuard, error) {
	interval := intervalOr(cfg.PollInterval)
	if interval < time.Second {
		interval = time.Second
	}
	runtime := cfg.RuntimeSession
	if runtime == "" {
		runtime = fmt.Sprintf("pid:%d", os.Getpid())
	}
	now := time.Now()
	record := AttachmentRecord{
		Host:        cfg.Host,
		Runtime:     runtime,
		PID:         os.Getpid(),
		FleetID:     fleetID,
		StartedAt:   now,
		HeartbeatAt: now,
		ExpiresAt:   now.Add(3 * interval),
	}
	acquired, err := AcquireAttachment(w.Home, record)
	if err != nil {
		return nil, fmt.Errorf("claim bridge attachment: %w", err)
	}
	if !acquired {
		return nil, ErrBridgeOwned
	}

	guard := &bridgeGuard{stopc: make(chan struct{}), errc: make(chan error, 1)}
	guard.stop = sync.OnceFunc(func() {
		close(guard.stopc)
		ClearAttachment(w.Home, cfg.Host, runtime)
	})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-guard.stopc:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				record.HeartbeatAt = now
				record.ExpiresAt = now.Add(3 * interval)
				ours, refreshErr := RefreshAttachment(w.Home, record)
				if refreshErr != nil {
					guard.errc <- fmt.Errorf("attachment heartbeat: %w", refreshErr)
					return
				}
				if !ours {
					guard.errc <- ErrBridgeOwned
					return
				}
			}
		}
	}()
	return guard, nil
}

// Advances one poll interval, aborting early on cancellation or a bridge-guard
// fault so a stolen or unwritable attachment never reads as a healthy wait.
func waitStep(ctx context.Context, guard *bridgeGuard, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case err := <-guard.errc:
		return err
	case <-timer.C:
		return nil
	}
}

// Serves the case where another watcher owns the fleet home: stealing its
// ownership would break that arm, so this wait levels on authoritative
// evidence instead of watcher edges.
func pollUntilEligible(ctx context.Context, w Waiter, cfg WaitConfig) (Wake, error) {
	wake, _, _, err := currentEpisodes(ctx, w, cfg.Host)
	if err != nil {
		return Wake{}, err
	}
	guard, err := acquireBridge(ctx, w, cfg, wake.FleetID)
	if err != nil {
		return Wake{}, err
	}
	defer guard.stop()
	for {
		if err := waitStep(ctx, guard, intervalOr(cfg.PollInterval)); err != nil {
			return Wake{}, err
		}
		wake, _, eligible, err := currentEpisodes(ctx, w, cfg.Host)
		if err != nil {
			return Wake{}, err
		}
		if len(eligible) > 0 {
			wake, won, claimErr := claimAndDeliver(w, cfg, wake, eligible)
			if claimErr != nil || won {
				return wake, claimErr
			}
			// Lost the transaction race: the holder keeps delivering; keep
			// observing for new work.
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

	wake, _, _, err := currentEpisodes(ctx, w, cfg.Host)
	if err != nil {
		return Wake{}, err
	}
	guard, err := acquireBridge(ctx, w, cfg, wake.FleetID)
	if err != nil {
		return Wake{}, err
	}
	defer guard.stop()

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
			wake, won, claimErr := claimAndDeliver(w, cfg, wake, eligible)
			if claimErr != nil || won {
				return wake, claimErr
			}
			// Lost the transaction race: re-arm and observe again instead of
			// answering an empty success.
		}
		keys := Keys(episodes)
		if slices.Equal(keys, previous) {
			if err := waitStep(ctx, guard, intervalOr(cfg.PollInterval)); err != nil {
				return Wake{}, err
			}
		}
		previous = keys
	}
}

// Performs the atomic eligibility-and-request transaction. Won=false means
// another waiter claimed inside the transaction: callers keep waiting and
// never answer an empty success.
func claimAndDeliver(w Waiter, cfg WaitConfig, wake Wake, all []Episode) (Wake, bool, error) {
	claimed, claimErr := w.Ledger.ClaimEligible(all)
	if claimErr != nil {
		return Wake{}, false, claimErr
	}
	if len(claimed) == 0 {
		return Wake{}, false, nil
	}
	wake.Schema = WakeSchema
	wake.Episodes = claimed
	wake.Message = WakeText(wake.FleetID)
	return wake, true, nil
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

// Owns one runtime's live attachment: stop() clears the record; errc carries
// the first heartbeat fault or ownership theft.
type bridgeGuard struct {
	stopc chan struct{}
	stop  func()
	errc  chan error
}
