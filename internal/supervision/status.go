package supervision

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/watcher"
)

func osEnv(key string) string { return os.Getenv(key) }

// Capability states for unattended Supervisor turn delivery, per harness.
// A product may support Worker execution while failing turn delivery, so the
// two are never collapsed into one supported boolean.
const (
	CapabilitySupported   = "supported"
	CapabilityDegraded    = "degraded"
	CapabilityUnsupported = "unsupported"
)

// Status is the typed integration diagnostic surface. Every field answers one
// separate operator question; there is deliberately no single supervision
// healthy boolean that could erase which stage is or is not working.
type Status struct {
	Harness       string
	HarnessSource string

	// RuntimeIdentity says whether the exact live Supervisor runtime can be
	// addressed where the host supports it (a Codex thread ID, for example).
	RuntimeIdentity string
	RuntimeDetail   string

	// Integration is the bootstrap/install state of Hand-owned host glue:
	// installed, unchanged, stale, absent, conflict, not-required.
	Integration       string
	IntegrationDetail string

	// WakeDeliveryCapability is whether this host can convert a Hand wake into
	// a reasoning opportunity without an operator message at all.
	WakeDelivery       string
	WakeDeliveryReason string

	// Attachment is whether a wake bridge is currently armed/attached where
	// that concept applies. Watcher liveness is reported separately.
	Attachment string

	// LastAccepted and LastOriented are ledger stamps: acceptance alone is
	// never progress; only orient demonstrates reasoning re-entry.
	LastAccepted *time.Time
	LastOriented *time.Time

	WatcherLiveness string
}

// StatusInput carries what status assembly cannot discover itself.
type StatusInput struct {
	Home      string
	Detection harness.Detection
	Exe       string
}

// Names the wake mechanism each harness relies on; the entries with no
// installed file have bridges that are instructions- or runtime-owned.
var hostMechanisms = map[string]string{
	harness.Claude:   "Stop hook async rewake",
	harness.Codex:    "same-thread codex queue",
	harness.OpenCode: "persistent TUI plugin synchronous prompt",
	harness.Pi:       "extension follow-up with turn trigger",
	harness.Grok:     "host-owned background monitor completion",
}

// IntegrationStatus assembles the typed diagnostics for one fleet home. The
// Codex capability probes the installed CLI's queue behavior, not its product
// name; every other host's capability follows from its mechanism.
func IntegrationStatus(ctx context.Context, in StatusInput) (Status, error) {
	status := Status{
		Harness:       in.Detection.Name,
		HarnessSource: in.Detection.Source,
	}
	if in.Detection.Name == "" {
		status.WakeDelivery = CapabilityUnsupported
		status.WakeDeliveryReason = "no Supervisor Harness is detected in this runtime"
		status.Integration = "absent"
		return status, nil
	}

	switch in.Detection.Name {
	case harness.Codex:
		threadID := osEnv(CodexThreadEnv)
		if threadID == "" {
			status.RuntimeIdentity = "unidentified"
			status.RuntimeDetail = fmt.Sprintf("%s is not set, so the live Codex thread cannot be addressed", CodexThreadEnv)
			status.WakeDelivery = CapabilityDegraded
			status.WakeDeliveryReason = "wake can be requested but no live thread is addressable from this runtime"
		} else {
			status.RuntimeIdentity = "identified"
			status.RuntimeDetail = threadID
			if err := ProbeCodexQueue(ctx, RunCommand); err != nil {
				status.WakeDelivery = CapabilityUnsupported
				status.WakeDeliveryReason = err.Error()
				break
			}
			status.WakeDelivery = CapabilitySupported
		}
		status.Integration = "not-required"

	case harness.Claude:
		result, err := CheckClaudeStopHook(in.Home, in.Exe)
		if err != nil {
			return status, err
		}
		status.Integration = result.State
		status.IntegrationDetail = result.Detail
		setFileBackedDelivery(&status, result.State)

	case harness.OpenCode, harness.Pi:
		results, err := CheckHostAssets(in.Home, in.Detection.Name, in.Exe)
		if err != nil {
			return status, err
		}
		status.Integration = aggregateAssetState(results)
		for _, result := range results {
			if result.Detail != "" {
				status.IntegrationDetail = result.Detail
			}
		}
		setFileBackedDelivery(&status, status.Integration)

	case harness.Grok:
		status.Integration = "not-required"
		status.IntegrationDetail = "the Grok bridge is the host background-task lifecycle, established through generated Supervisor instructions"
		status.WakeDelivery = CapabilitySupported
		status.WakeDeliveryReason = hostMechanisms[harness.Grok]

	default:
		status.WakeDelivery = CapabilityUnsupported
		status.WakeDeliveryReason = fmt.Sprintf("harness %q has no qualified unattended turn delivery path", in.Detection.Name)
		status.Integration = "absent"
	}

	attached, err := watcher.IsAttached(in.Home)
	switch {
	case err != nil:
		status.Attachment = "unknown"
		status.WatcherLiveness = "unknown"
	case attached:
		status.Attachment = "attached"
		status.WatcherLiveness = "alive"
	default:
		status.Attachment = "detached"
		status.WatcherLiveness = "idle"
	}

	lastAccepted, lastOriented := OpenLedger(in.Home).Progress()
	status.LastAccepted = lastAccepted
	status.LastOriented = lastOriented
	return status, nil
}

func setFileBackedDelivery(status *Status, integrationState string) {
	status.WakeDelivery = CapabilitySupported
	status.WakeDeliveryReason = hostMechanisms[status.Harness]
	if integrationState != "installed" && integrationState != "unchanged" {
		status.WakeDelivery = CapabilityDegraded
		status.WakeDeliveryReason = fmt.Sprintf("%s integration is %s; run hand init to repair it", status.Harness, integrationState)
	}
}

func aggregateAssetState(results []InstallResult) string {
	state := "installed"
	for _, result := range results {
		switch result.State {
		case "conflict":
			return "conflict"
		case "stale", "absent":
			state = result.State
		}
	}
	return state
}
