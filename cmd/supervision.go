package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/orientation"
	"github.com/atqamz/hand/internal/supervision"
	"github.com/atqamz/hand/internal/watcher"
	"github.com/spf13/cobra"
)

// The default bound for a Stop-hook wait cycle: long enough to hold through a
// normal work stretch, bounded so the hook lifecycle always gets its child
// back. The upstream hook timeout must stay above it.
const defaultStopHookTimeout = 30 * time.Minute

// The cooldown that keeps a deterministic bridge failure from becoming a
// per-turn feedback loop.
const bridgeFailureCooldown = supervision.BridgeFailureCooldown

func newSupervisionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "supervision",
		Short:  "Supervisor wake coordination internals",
		Hidden: true,
	}
	cmd.AddCommand(newSupervisionWaitCmd())
	cmd.AddCommand(newClaudeStopCmd())
	cmd.AddCommand(newCodexStopCmd())
	cmd.AddCommand(newSupervisionReceiptCmd())
	return cmd
}

// The provider-neutral wait primitive host bridges call. Hidden and
// internal-facing, but its structured output and typed exits are a contract:
// 0 wake, 4 checkpoint, 3 owned elsewhere, 5 failure, 8 interrupted, 9 replaced.
func newSupervisionWaitCmd() *cobra.Command {
	var host string
	var timeout time.Duration
	var deliver bool
	var format string

	cmd := &cobra.Command{
		Use:   "wait --host <claude|codex|opencode|pi|grok>",
		Short: "Block until current actionable work is wake-eligible, then emit one coalesced wake",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv(harness.RoleEnv) == harness.WorkerRole {
				return &ExitError{Err: fmt.Errorf("supervision wait is unavailable when %s=%s", harness.RoleEnv, harness.WorkerRole), Code: 3}
			}
			if !supervision.IsSupervisorHost(host) {
				return &ExitError{Err: fmt.Errorf("invalid --host %q: want one of %s", host, strings.Join(supervision.SupervisorHosts(), ", ")), Code: 2}
			}
			if cmd.Flags().Changed("timeout") && timeout <= 0 {
				return &ExitError{Err: fmt.Errorf("invalid timeout %s: must be positive", timeout), Code: 2}
			}
			switch format {
			case "", "toon", "json":
			default:
				return &ExitError{Err: fmt.Errorf("invalid --format %q: want toon or json", format), Code: 2}
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
				RuntimeSession: os.Getenv("HAND_RUNTIME_SESSION"),
				PollInterval:   pollInterval,
				StaleThreshold: staleThreshold,
				ParkedBounds:   parkedBounds,
				Timeout:        timeout,
			})
			if errors.Is(err, supervision.ErrBridgeOwned) {
				return &ExitError{Err: err, Code: 3}
			}
			if err != nil {
				return mapWatchResult(err)
			}

			if format == "json" {
				doc := struct {
					Schema   string                       `json:"schema"`
					FleetID  string                       `json:"fleet_id"`
					Host     string                       `json:"host"`
					Message  string                       `json:"message"`
					Episodes []supervision.EpisodePayload `json:"episodes"`
				}{
					Schema:   supervision.WakeSchema,
					FleetID:  wake.FleetID,
					Host:     wake.Host,
					Message:  wake.Message,
					Episodes: supervision.EpisodePayloads(wake.Episodes),
				}
				encoded, encodeErr := json.MarshalIndent(doc, "", "  ")
				if encodeErr != nil {
					return encodeErr
				}
				_, writeErr := fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
				return writeErr
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
			doc.Field("text", wake.Message)
			if deliver && host == harness.Codex {
				appendCodexDelivery(&doc, cmd.Context(), wake)
			}
			return doc.Render(cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&host, "host", "", "the Supervisor Harness runtime arming this wait")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "give up after this long and exit 4 (default: no timeout)")
	cmd.Flags().BoolVar(&deliver, "deliver", false, "with --host codex, enqueue the wake on the live thread via codex queue after eligibility")
	cmd.Flags().StringVar(&format, "format", "", "output protocol: toon (human) or json (versioned machine contract)")
	return cmd
}

// The Hand-owned Claude Stop entry point, run with asyncRewake so Claude
// executes it in the background at turn end without blocking Stop. Exit 2
// with bounded stderr is the rewake into a follow-up `hand orient` turn.
func newClaudeStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "claude-stop",
		Short:  "One background wait cycle behind the Claude Stop hook (asyncRewake)",
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
			timeout := stopHookTimeoutFromEnv(defaultStopHookTimeout)
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
				RuntimeSession: hookSessionID(cmd.InOrStdin()),
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
				fmt.Fprintln(os.Stderr, wake.Message)
				return &ExitError{Err: errors.New(wake.Message), Code: 2}
			case errors.Is(waitErr, watcher.ErrNoEvent), errors.Is(waitErr, watcher.ErrInterrupted), errors.Is(waitErr, watcher.ErrReplaced):
				return nil
			case errors.Is(waitErr, supervision.ErrBridgeOwned):
				return nil
			case errors.Is(waitErr, watcher.ErrArmFailed):
				if !ledger.BridgeErroredBefore(harness.Claude, bridgeFailureCooldown) {
					return nil
				}
				if markErr := ledger.MarkBridgeError(harness.Claude, waitErr.Error()); markErr != nil {
					return markErr
				}
				// `hand doctor` never inspects pane reachability, so it cannot see this condition -
				// `hand status` is what actually observes an unreachable pane (atqamz/hand#455).
				return &ExitError{Err: fmt.Errorf("hand supervisor wake bridge failed monitoring: %v; run `hand status` in the fleet home to see which task's pane is unreachable", waitErr), Code: 2}
			default:
				return waitErr
			}
		},
	}
}

// The Hand-owned Codex Stop entry, installed Fleet-locally as an async
// background handler. Codex's hook lifecycle runs it at every turn end, so it
// owns the post-turn wait and enqueues the wake on the exact live thread.
func newCodexStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "codex-stop",
		Short:  "Background wait-and-queue cycle behind the Codex project Stop hook",
		Hidden: true,
		Args:   usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv(harness.RoleEnv) == harness.WorkerRole {
				return &ExitError{Err: fmt.Errorf("supervision codex-stop is unavailable when %s=%s", harness.RoleEnv, harness.WorkerRole), Code: 3}
			}
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			threadID := hookSessionID(cmd.InOrStdin())
			if threadID == "" {
				threadID = os.Getenv(supervision.CodexThreadEnv)
			}
			if strings.TrimSpace(threadID) == "" {
				return nil
			}
			timeout := stopHookTimeoutFromEnv(defaultStopHookTimeout)
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
				Host:           harness.Codex,
				RuntimeSession: threadID,
				PollInterval:   pollInterval,
				StaleThreshold: staleThreshold,
				ParkedBounds:   parkedBounds,
				Timeout:        timeout,
			})
			switch {
			case waitErr == nil:
				if deliverErr := supervision.DeliverCodexQueue(cmd.Context(), supervision.RunCommand, threadID, wake.Message); deliverErr != nil {
					_ = ledger.MarkBridgeError(harness.Codex, deliverErr.Error())
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "hand supervisor wake:", deliverErr.Error())
					return nil
				}
				// Queue acceptance is enqueued-only; the resulting turn's hand
				// orient records the reasoning progress.
				return ledger.MarkAccepted(supervision.Keys(wake.Episodes))
			case errors.Is(waitErr, watcher.ErrNoEvent), errors.Is(waitErr, watcher.ErrInterrupted),
				errors.Is(waitErr, watcher.ErrReplaced), errors.Is(waitErr, supervision.ErrBridgeOwned):
				return nil
			default:
				if !ledger.BridgeErroredBefore(harness.Codex, bridgeFailureCooldown) {
					return nil
				}
				_ = ledger.MarkBridgeError(harness.Codex, waitErr.Error())
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "hand supervisor wake: monitoring failed:", waitErr.Error())
				return nil
			}
		},
	}
}

// receipt records a host adapter's delivery evidence against episodes in the
// disposable mechanism ledger. Acceptance means the host took the wake;
// neither stage ever implies reasoning - only hand orient does.
func newSupervisionReceiptCmd() *cobra.Command {
	var host, stage, detail string
	var episodes []string
	cmd := &cobra.Command{
		Use:    "receipt --host <h> --stage accepted|delivery-failed --episode <key>...",
		Short:  "Record one host adapter's delivery receipt",
		Hidden: true,
		Args:   usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv(harness.RoleEnv) == harness.WorkerRole {
				return &ExitError{Err: fmt.Errorf("supervision receipt is unavailable when %s=%s", harness.RoleEnv, harness.WorkerRole), Code: 3}
			}
			if !supervision.IsSupervisorHost(host) {
				return &ExitError{Err: fmt.Errorf("invalid --host %q", host), Code: 2}
			}
			fleetHome, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			ledger := supervision.OpenLedger(fleetHome)
			switch stage {
			case "accepted":
				return ledger.MarkAccepted(episodes)
			case "delivery-failed":
				if len(episodes) == 0 {
					return ledger.MarkBridgeError(host, orDash(detail))
				}
				return ledger.MarkErrorByKeys(episodes, detail)
			default:
				return &ExitError{Err: fmt.Errorf("invalid --stage %q: want accepted or delivery-failed", stage), Code: 2}
			}
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "host whose adapter produced this receipt")
	cmd.Flags().StringVar(&stage, "stage", "", "accepted or delivery-failed")
	cmd.Flags().StringSliceVar(&episodes, "episode", nil, "claimed episode keys this receipt covers")
	cmd.Flags().StringVar(&detail, "detail", "", "bounded failure evidence")
	return cmd
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "host reported delivery failure"
	}
	return s
}

// Reads the hook stdin payload's session identifier without requiring it:
// both Claude and Codex pass hook context as JSON on stdin.
func hookSessionID(in io.Reader) string {
	if in == nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(in, 1<<20))
	if err != nil || len(data) == 0 {
		return ""
	}
	var payload struct {
		SessionID string `json:"session_id"`
		ThreadID  string `json:"thread_id"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return ""
	}
	if payload.SessionID != "" {
		return payload.SessionID
	}
	return payload.ThreadID
}

func stopHookTimeoutFromEnv(fallback time.Duration) time.Duration {
	raw := os.Getenv("HAND_SUPERVISION_WAIT_TIMEOUT")
	if raw == "" {
		return fallback
	}
	parsed, parseErr := time.ParseDuration(raw)
	if parseErr != nil || parsed <= 0 {
		return fallback
	}
	return parsed
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
	err := supervision.DeliverCodexQueue(ctx, supervision.RunCommand, os.Getenv(supervision.CodexThreadEnv), wake.Message)
	switch {
	case err == nil:
		doc.Field("delivery", "accepted")
	case errors.Is(err, supervision.ErrUnsupported):
		doc.Field("delivery", "unsupported")
		doc.Field("delivery_reason", err.Error())
	default:
		doc.Field("delivery", "rejected")
		doc.Field("delivery_reason", err.Error())
	}
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
	for _, host := range supervision.ManagedAssetHosts() {
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

func watchConfigFromFleet(fleetHome string) (time.Duration, time.Duration, watcher.ParkedBounds, error) {
	pollInterval, err := time.ParseDuration(configDefault(fleetHome, "watch-interval", "5s"))
	if err != nil {
		return 0, 0, watcher.ParkedBounds{}, fmt.Errorf("invalid watch-interval: %w", err)
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
