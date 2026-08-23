package supervision

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/watcher"
)

func osExecutableOrUnknown() string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return "unknown"
	}
	return exe
}

func osEnv(key string) string { return os.Getenv(key) }

// Capability states for unattended Supervisor turn delivery. A product may
// support Worker execution while failing turn delivery; supported means
// live-qualified, and everything before that is available-unqualified.
const (
	CapabilitySupported   = "supported"
	CapabilityUnqualified = "available-unqualified"
	CapabilityDegraded    = "degraded"
	CapabilityUnsupported = "unsupported"
)

// Hosts whose exact runtime path has passed live no-operator-input dogfood.
// Deliberately empty until that evidence exists; qualification flips a host
// from available-unqualified to supported, never source-code intent.
var qualifiedHosts = map[string]bool{}

// The Supervisor turn-delivery registry, distinct from harness's Worker
// launch support: a name may build workers while having no wake bridge, and
// only these five carry supervisor semantics.
var supervisorHosts = []string{
	harness.Claude,
	harness.Codex,
	harness.Grok,
	harness.Pi,
	harness.OpenCode,
}

// IsSupervisorHost reports whether name belongs to the Supervisor
// turn-delivery registry.
func IsSupervisorHost(name string) bool {
	for _, host := range supervisorHosts {
		if host == name {
			return true
		}
	}
	return false
}

// SupervisorHosts returns the registry in stable order.
func SupervisorHosts() []string {
	return append([]string(nil), supervisorHosts...)
}

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

	// WakeDelivery is whether this host converts a wake into a reasoning
	// opportunity unattended - and at which confidence: supported means
	// live-qualified, available-unqualified means static preconditions only.
	WakeDelivery       string
	WakeDeliveryReason string

	// Attachment is whether THIS host's wait bridge is currently held by a
	// live runtime of THIS Fleet, read from heartbeat evidence - never from
	// watcher liveness, which is reported separately below.
	Attachment string

	// LastAccepted and LastOriented are ledger stamps: acceptance alone is
	// never progress; only orient demonstrates reasoning re-entry.
	LastAccepted *time.Time
	LastOriented *time.Time

	// WatcherLiveness answers exactly one question: is a fleet observer
	// process alive? It says nothing about wake delivery.
	WatcherLiveness string
}

// StatusInput carries what status assembly cannot discover itself.
type StatusInput struct {
	Home      string
	Detection harness.Detection
	Exe       string
}

// The wake mechanism each registry host relies on.
var hostMechanisms = map[string]string{
	harness.Claude:   "Stop hook asyncRewake",
	harness.Codex:    "same-thread codex queue driven by project-scope Stop hook",
	harness.OpenCode: "persistent TUI plugin synchronous prompt",
	harness.Pi:       "extension mechanism follow-up with turn trigger",
	harness.Grok:     "host-owned background monitor completion",
}

// IntegrationStatus assembles the typed diagnostics for one fleet home: the
// Codex capability probes installed queue behavior rather than product name,
// file-backed hosts degrade on drift, nothing claims supported pre-proof.
func IntegrationStatus(ctx context.Context, in StatusInput) (Status, error) {
	status := Status{
		Harness:       in.Detection.Name,
		HarnessSource: in.Detection.Source,
	}
	if !IsSupervisorHost(in.Detection.Name) {
		if in.Detection.Name == "" {
			status.WakeDelivery = CapabilityUnsupported
			status.WakeDeliveryReason = "no Supervisor Harness is detected in this runtime"
		} else {
			status.WakeDelivery = CapabilityUnsupported
			status.WakeDeliveryReason = fmt.Sprintf("harness %q has worker support but no supervisor turn-delivery path", in.Detection.Name)
		}
		status.Integration = "absent"
		status.finishIndependentFields(in)
		return status, nil
	}

	switch in.Detection.Name {
	case harness.Codex:
		threadID := osEnv(CodexThreadEnv)
		if threadID == "" {
			status.RuntimeIdentity = "unidentified"
			status.RuntimeDetail = fmt.Sprintf("%s is not set, so the live Codex thread cannot be addressed", CodexThreadEnv)
		} else {
			status.RuntimeIdentity = "identified"
			status.RuntimeDetail = threadID
		}
		status.Integration = inspectCodexHooks(codexHooksPath(in.Home), osExecutableOrUnknown()).State
		probeErr := ProbeCodexQueue(ctx, RunCommand)
		switch {
		case status.Integration != "installed" && status.Integration != "unchanged":
			status.WakeDelivery = CapabilityDegraded
			status.WakeDeliveryReason = fmt.Sprintf("codex Stop-hook integration is %s; run hand init to repair it, then approve it under /hooks", status.Integration)
		case probeErr != nil:
			status.WakeDelivery = CapabilityUnsupported
			status.WakeDeliveryReason = probeErr.Error()
		case threadID == "":
			status.WakeDelivery = CapabilityDegraded
			status.WakeDeliveryReason = "wake can be requested but no live thread is addressable from this runtime"
		default:
			status.WakeDelivery = CapabilityUnqualified
			status.WakeDeliveryReason = hostMechanisms[harness.Codex] + "; requires features.hooks enabled and /hooks trust approval"
		}

	case harness.Claude:
		result, err := CheckClaudeStopHook(in.Home, in.Exe)
		if err != nil {
			status.Integration = result.State
			status.IntegrationDetail = result.Detail
			status.WakeDelivery = CapabilityDegraded
			status.WakeDeliveryReason = fmt.Sprintf("claude integration is %s: %s; run hand init to repair it", result.State, result.Detail)
			break
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
		status.WakeDelivery = CapabilityUnqualified
		status.WakeDeliveryReason = hostMechanisms[harness.Grok] + "; instruction-established, live qualification pending"
	}

	status.finishIndependentFields(in)
	return status, nil
}

// Fills the two mechanically independent liveness answers: bridge attachment
// comes from this Fleet's heartbeat record, watcher liveness from fleet-home
// ownership. Neither implies the other.
func (s *Status) finishIndependentFields(in StatusInput) {
	record := ReadAttachment(in.Home)
	switch {
	case record != nil && record.Fresh(time.Now()) && (record.Host == s.Harness || s.Harness == ""):
		s.Attachment = "attached"
		if record.Runtime != "" {
			s.Attachment += ":" + record.Runtime
		}
	default:
		s.Attachment = "detached"
	}

	attached, err := watcher.IsAttached(in.Home)
	switch {
	case err != nil:
		s.WatcherLiveness = "unknown"
	case attached:
		s.WatcherLiveness = "alive"
	default:
		s.WatcherLiveness = "idle"
	}

	lastAccepted, lastOriented := OpenLedger(in.Home).Progress()
	s.LastAccepted = lastAccepted
	s.LastOriented = lastOriented
}

func setFileBackedDelivery(status *Status, integrationState string) {
	status.WakeDelivery = CapabilityUnqualified
	status.WakeDeliveryReason = hostMechanisms[status.Harness] + "; live qualification pending"
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
