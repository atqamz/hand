package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/orientation"
	"github.com/atqamz/hand/internal/supervision"
	"github.com/atqamz/hand/internal/watcher"
	"github.com/spf13/cobra"
)

// The default bound for the Claude Stop hook's single wait cycle: long enough
// to hold through a normal work stretch, bounded so a turn can always end.
const defaultStopHookTimeout = 10 * time.Minute

// The cooldown that keeps a deterministic bridge failure from becoming a
// per-turn feedback loop.
const bridgeFailureCooldown = time.Hour

func newSupervisionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "supervision",
		Short:  "Supervisor wake coordination internals",
		Hidden: true,
	}
	cmd.AddCommand(newSupervisionWaitCmd())
	cmd.AddCommand(newClaudeStopCmd())
	return cmd
}

// The provider-neutral wait primitive host bridges call. Hidden, but its
// structured output and typed exits are a contract: 0 eligible wake, 4
// checkpoint, 5 monitoring failure, 8 interrupted, 9 replaced.
func newSupervisionWaitCmd() *cobra.Command {
	var host string
	var timeout time.Duration
	var deliver bool

	cmd := &cobra.Command{
		Use:   "wait --host <claude|codex|opencode|pi|grok>",
		Short: "Block until current actionable work is wake-eligible, then emit one coalesced wake",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv(harness.RoleEnv) == harness.WorkerRole {
				return &ExitError{Err: fmt.Errorf("supervision wait is unavailable when %s=%s", harness.RoleEnv, harness.WorkerRole), Code: 3}
			}
			if !harness.IsSupported(host) {
				return &ExitError{Err: fmt.Errorf("invalid --host %q: want one of %s", host, joinHarnesses()), Code: 2}
			}
			if cmd.Flags().Changed("timeout") && timeout <= 0 {
				return &ExitError{Err: fmt.Errorf("invalid timeout %s: must be positive", timeout), Code: 2}
			}
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			pollInterval, staleThreshold, parkedBounds, err := watchConfigFromFleet(fleetHome)
			if err != nil {
				return err
			}

			wake, err := supervision.Wait(cmd.Context(), supervision.Waiter{
				Home:         fleetHome,
				ReadEvidence: fleetEvidenceReader(cmd, fleetHome),
				Ledger:       supervision.OpenLedger(fleetHome),
			}, supervision.WaitConfig{
				Host:           host,
				PollInterval:   pollInterval,
				StaleThreshold: staleThreshold,
				ParkedBounds:   parkedBounds,
				Timeout:        timeout,
			})
			if err != nil {
				return mapWatchResult(err)
			}

			var doc axi.Doc
			doc.Field("woke", "true")
			doc.Field("host", wake.Host)
			doc.Field("fleet_id", wake.FleetID)
			doc.Int("episodes", len(wake.Episodes))
			rows := make([][]string, 0, len(wake.Episodes))
			for _, episode := range wake.Episodes {
				rows = append(rows, []string{episode.TargetID, episode.TargetKind, episode.Currentness.String(), episode.Kind})
			}
			doc.Rows("wake_episodes", []string{"target_id", "kind", "currentness", "action"}, rows)
			doc.Field("text", wake.Text)
			if deliver && host == harness.Codex {
				appendCodexDelivery(&doc, cmd.Context(), wake)
			}
			return doc.Render(cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&host, "host", "", "the Supervisor Harness runtime arming this wait")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "give up after this long and exit 4 (default: no timeout)")
	cmd.Flags().BoolVar(&deliver, "deliver", false, "with --host codex, enqueue the wake on the live thread via codex queue after eligibility")
	return cmd
}

// The Hand-owned Claude Stop hook entry point. Exit 2 with one bounded stderr
// line is the Stop-hook rewake: it becomes a follow-up turn whose first Hand
// read is `hand orient`. Silence lets the turn end when nothing is eligible.
func newClaudeStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "claude-stop",
		Short:  "One bounded wait cycle behind the Claude Stop hook",
		Hidden: true,
		Args:   usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv(harness.RoleEnv) == harness.WorkerRole {
				return &ExitError{Err: fmt.Errorf("supervision claude-stop is unavailable when %s=%s", harness.RoleEnv, harness.WorkerRole), Code: 3}
			}
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			timeout := defaultStopHookTimeout
			if raw := os.Getenv("HAND_SUPERVISION_WAIT_TIMEOUT"); raw != "" {
				parsed, parseErr := time.ParseDuration(raw)
				if parseErr != nil {
					return fmt.Errorf("parse HAND_SUPERVISION_WAIT_TIMEOUT %q: %w", raw, parseErr)
				}
				timeout = parsed
			}
			pollInterval, staleThreshold, parkedBounds, err := watchConfigFromFleet(fleetHome)
			if err != nil {
				return err
			}
			ledger := supervision.OpenLedger(fleetHome)

			wake, waitErr := supervision.Wait(cmd.Context(), supervision.Waiter{
				Home:         fleetHome,
				ReadEvidence: fleetEvidenceReader(cmd, fleetHome),
				Ledger:       ledger,
			}, supervision.WaitConfig{
				Host:           harness.Claude,
				PollInterval:   pollInterval,
				StaleThreshold: staleThreshold,
				ParkedBounds:   parkedBounds,
				Timeout:        timeout,
			})
			switch {
			case waitErr == nil:
				// Requesting the rewake here is acceptance by this bridge; the
				// resulting turn running hand orient remains the only progress
				// witness, recorded by orient itself.
				if err := ledger.MarkAccepted(supervision.Keys(wake.Episodes)); err != nil {
					return err
				}
				return &ExitError{Err: errors.New(wake.Text), Code: 2}
			case errors.Is(waitErr, watcher.ErrNoEvent), errors.Is(waitErr, watcher.ErrInterrupted), errors.Is(waitErr, watcher.ErrReplaced):
				return nil
			case errors.Is(waitErr, watcher.ErrArmFailed):
				if !ledger.BridgeErroredBefore(harness.Claude, bridgeFailureCooldown) {
					return nil
				}
				if markErr := ledger.MarkBridgeError(harness.Claude, waitErr.Error()); markErr != nil {
					return markErr
				}
				return &ExitError{Err: fmt.Errorf("hand supervisor wake bridge failed monitoring: %v; run `hand doctor` in the fleet home", waitErr), Code: 2}
			default:
				return waitErr
			}
		},
	}
}

// Builds the fresh unbounded actionable-evidence reader every wait cycle
// re-derives from authoritative Fleet state.
func fleetEvidenceReader(cmd *cobra.Command, fleetHome string) supervision.EvidenceReader {
	warnOut := cmd.ErrOrStderr()
	return func(ctx context.Context) (orientation.Evidence, error) {
		snapshot, err := loadFleetSnapshot(ctx, warnOut, fleetHome)
		if err != nil {
			return orientation.Evidence{}, err
		}
		return snapshot.evidence(), nil
	}
}

func appendCodexDelivery(doc *axi.Doc, ctx context.Context, wake supervision.Wake) {
	threadID := os.Getenv(supervision.CodexThreadEnv)
	deliverErr := supervision.DeliverCodexQueue(ctx, supervision.RunCommand, threadID, wake.Text)
	switch {
	case deliverErr == nil:
		doc.Field("delivery", "accepted")
		doc.Field("runtime_thread", threadID)
	case errors.Is(deliverErr, supervision.ErrUnsupported):
		doc.Field("delivery", "unsupported")
		doc.Field("delivery_reason", deliverErr.Error())
	default:
		doc.Field("delivery", "rejected")
		doc.Field("delivery_reason", deliverErr.Error())
	}
}

func joinHarnesses() string {
	out := ""
	for i, name := range harness.Names() {
		if i > 0 {
			out += ", "
		}
		out += name
	}
	return out
}

func watchConfigFromFleet(fleetHome string) (time.Duration, time.Duration, watcher.ParkedBounds, error) {
	pollInterval, err := time.ParseDuration(configDefault(fleetHome, "watch-interval", "5s"))
	if err != nil {
		return 0, 0, watcher.ParkedBounds{}, fmt.Errorf("invalid watch-interval %q: %w", pollInterval, err)
	}
	staleThreshold, err := configSeconds(fleetHome, "stale-threshold", defaultStaleThreshold)
	if err != nil {
		return 0, 0, watcher.ParkedBounds{}, err
	}
	bounds, err := parkedBoundsFromConfig(fleetHome)
	if err != nil {
		return 0, 0, watcher.ParkedBounds{}, err
	}
	return pollInterval, staleThreshold, bounds, nil
}

var supervisionInstallFields = []axi.Column[supervision.InstallResult]{
	{Name: "host", Value: func(r supervision.InstallResult) string { return r.Host }},
	{Name: "path", Value: func(r supervision.InstallResult) string { return r.Path }},
	{Name: "state", Value: func(r supervision.InstallResult) string { return r.State }},
	{Name: "detail", Value: func(r supervision.InstallResult) string { return r.Detail }},
}

// Installs every host bridge hand manages, so whichever harness a supervisor
// later runs in this home just works without hand-copying glue. Foreign
// content at an exact managed path is a surfaced conflict, never an overwrite.
func installSupervisorBridgesForInit(fleetHome, exe string) ([]supervision.InstallResult, []string) {
	var results []supervision.InstallResult
	var conflicts []string
	claudeResult, err := supervision.InstallClaudeStopHook(fleetHome, exe)
	if err != nil {
		conflicts = append(conflicts, fmt.Sprintf("%s: %s", claudeResult.Path, err.Error()))
	}
	results = append(results, claudeResult)
	for _, host := range []string{harness.OpenCode, harness.Pi} {
		assetResults, err := supervision.InstallHostAssets(fleetHome, host, exe)
		if err != nil {
			conflicts = append(conflicts, err.Error())
			continue
		}
		results = append(results, assetResults...)
		for _, result := range assetResults {
			if result.State == "conflict" {
				conflicts = append(conflicts, result.Path+": "+result.Detail)
			}
		}
	}
	return results, conflicts
}
