package orientation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const Schema = "hand.supervisor.v1"

const maxOrientationItems = 64
const maxOrientationText = 256

type MonitorState string

const (
	MonitorStateArmed        MonitorState = "armed"
	MonitorStateRearmed      MonitorState = "rearmed"
	MonitorStateAlreadyArmed MonitorState = "already-armed"
	MonitorStateDegraded     MonitorState = "degraded"
	MonitorStateUnknown      MonitorState = "unknown"
)

type CurrentnessToken struct {
	value string
}

func (t CurrentnessToken) String() string {
	return t.value
}

func (t CurrentnessToken) IsZero() bool {
	return t.value == ""
}

func (t CurrentnessToken) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.value)
}

type MonitorTarget struct {
	ID          string           `json:"id"`
	Kind        string           `json:"kind"`
	Currentness CurrentnessToken `json:"currentness"`
}

type WorkSummary struct {
	ID       string        `json:"id"`
	Kind     string        `json:"kind"`
	State    string        `json:"state"`
	Reported string        `json:"reported,omitempty"`
	Monitor  MonitorTarget `json:"monitor"`
}

type ActionableSubject struct {
	Target     MonitorTarget `json:"target"`
	Kind       string        `json:"kind"`
	Reason     string        `json:"reason"`
	Provenance string        `json:"provenance"`
}

type DeferredSubject struct {
	TargetID   string `json:"target_id"`
	Kind       string `json:"kind"`
	Reason     string `json:"reason"`
	Provenance string `json:"provenance"`
}

type NextAction struct {
	Kind    string `json:"kind"`
	Target  string `json:"target,omitempty"`
	Command string `json:"command,omitempty"`
	Reason  string `json:"reason"`
}

type BoundedError struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

type SupervisorOrientation struct {
	Schema       string              `json:"schema"`
	FleetID      string              `json:"fleet_id"`
	Work         []WorkSummary       `json:"work"`
	Actionable   []ActionableSubject `json:"actionable"`
	Deferred     []DeferredSubject   `json:"deferred"`
	Monitors     []MonitorTarget     `json:"monitors"`
	MonitorState MonitorState        `json:"monitor_state"`
	NextActions  []NextAction        `json:"next_actions"`
	Truncated    bool                `json:"truncated"`
	Omitted      int                 `json:"omitted"`
	Errors       []BoundedError      `json:"errors"`
}

type TargetEvidence struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Generation []string `json:"generation"`
}

type TaskTargetFacts struct {
	ID               string
	Kind             string
	CreatedAt        string
	Lifecycle        string
	ActiveAttemptID  int64
	AttemptID        int64
	AttemptLifecycle string
	RuntimeIdentity  []string
	StatusChangedAt  string
	StatusChangedFor string
	ReportState      string
	ReportOffset     int64
	ReportDigest     string
	DoneVerified     bool
	PR               string
	MergeExecuted    bool
	MergeAnnounced   bool
}

type WorkEvidence struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	State    string `json:"state"`
	Reported string `json:"reported,omitempty"`
}

type ActionableEvidence struct {
	TargetID   string   `json:"target_id"`
	TargetKind string   `json:"target_kind"`
	Generation []string `json:"generation"`
	Kind       string   `json:"kind"`
	Reason     string   `json:"reason"`
	Provenance string   `json:"provenance"`
}

type Evidence struct {
	FleetID      string               `json:"fleet_id"`
	Work         []WorkEvidence       `json:"work"`
	Actionable   []ActionableEvidence `json:"actionable"`
	Deferred     []DeferredSubject    `json:"deferred"`
	Targets      []TargetEvidence     `json:"targets"`
	NextActions  []NextAction         `json:"next_actions"`
	MonitorState MonitorState         `json:"monitor_state"`
	Errors       []BoundedError       `json:"errors"`
}

type Reader func(context.Context) (Evidence, error)

type Provider struct {
	read Reader
}

func NewProvider(read Reader) *Provider {
	return &Provider{read: read}
}

func (p *Provider) Orientation(ctx context.Context) (SupervisorOrientation, error) {
	if p == nil || p.read == nil {
		return SupervisorOrientation{}, errors.New("orientation provider has no evidence reader")
	}
	evidence, err := p.read(ctx)
	if err != nil {
		return SupervisorOrientation{}, err
	}
	return Build(evidence), nil
}

func Build(evidence Evidence) SupervisorOrientation {
	result := SupervisorOrientation{Schema: Schema, FleetID: evidence.FleetID, MonitorState: evidence.MonitorState}
	result.MonitorState = normalizeMonitorState(result.MonitorState)
	if result.FleetID == "" {
		result.Errors = append(result.Errors, BoundedError{Kind: "fleet", Reason: "Fleet identity is unavailable"})
		result.MonitorState = MonitorStateUnknown
	}

	targets := make(map[string]MonitorTarget, len(evidence.Targets))
	for _, target := range evidence.Targets {
		key := targetKey(target.Kind, target.ID)
		if _, exists := targets[key]; exists {
			result.Errors = append(result.Errors, BoundedError{Kind: "target", Reason: fmt.Sprintf("duplicate monitor target %q", target.ID)})
			continue
		}
		targets[key] = TargetFor(evidence.FleetID, target)
	}

	seenTargets := make(map[string]bool, len(evidence.Targets))
	for _, target := range sortedTargetEvidence(evidence.Targets) {
		key := targetKey(target.Kind, target.ID)
		if seenTargets[key] {
			continue
		}
		seenTargets[key] = true
		if monitor, ok := targets[targetKey(target.Kind, target.ID)]; ok {
			result.Monitors = append(result.Monitors, monitor)
		}
	}
	for _, work := range sortedWork(evidence.Work) {
		monitor := targets[targetKey(work.Kind, work.ID)]
		if monitor.ID == "" {
			result.Errors = append(result.Errors, BoundedError{Kind: "work", Reason: fmt.Sprintf("work item %q has no monitor target", work.ID)})
		}
		result.Work = append(result.Work, WorkSummary{ID: work.ID, Kind: work.Kind, State: work.State, Reported: work.Reported, Monitor: monitor})
	}
	for _, item := range sortedActionable(evidence.Actionable) {
		monitor := targets[targetKey(item.TargetKind, item.TargetID)]
		if monitor.ID == "" {
			result.Errors = append(result.Errors, BoundedError{Kind: "actionable", Reason: fmt.Sprintf("actionable item %q has no monitor target", item.TargetID)})
		}
		result.Actionable = append(result.Actionable, ActionableSubject{Target: monitor, Kind: item.Kind, Reason: item.Reason, Provenance: item.Provenance})
	}
	result.Deferred = append(result.Deferred, sortedDeferred(evidence.Deferred)...)
	result.NextActions = sortedNextActions(evidence.NextActions)
	result.Errors = append(result.Errors, evidence.Errors...)
	sort.SliceStable(result.Errors, func(i, j int) bool {
		if result.Errors[i].Kind != result.Errors[j].Kind {
			return result.Errors[i].Kind < result.Errors[j].Kind
		}
		return result.Errors[i].Reason < result.Errors[j].Reason
	})
	bound(&result)
	return result
}

func normalizeMonitorState(state MonitorState) MonitorState {
	switch state {
	case MonitorStateArmed, MonitorStateRearmed, MonitorStateAlreadyArmed, MonitorStateDegraded:
		return state
	default:
		return MonitorStateUnknown
	}
}

func TargetFor(fleetID string, target TargetEvidence) MonitorTarget {
	return MonitorTarget{
		ID:          targetID(fleetID, target.Kind, target.ID),
		Kind:        target.Kind,
		Currentness: currentnessToken(fleetID, target.Kind, target.ID, target.Generation),
	}
}

func TaskTarget(fleetID string, facts TaskTargetFacts) MonitorTarget {
	return TargetFor(fleetID, TaskTargetEvidence(facts))
}

func TaskTargetEvidence(facts TaskTargetFacts) TargetEvidence {
	return TargetEvidence{ID: facts.ID, Kind: facts.Kind, Generation: taskGeneration(facts)}
}

func taskGeneration(facts TaskTargetFacts) []string {
	runtimeIdentity := append([]string(nil), facts.RuntimeIdentity...)
	for len(runtimeIdentity) < 4 {
		runtimeIdentity = append(runtimeIdentity, "")
	}
	return []string{
		facts.CreatedAt,
		facts.Lifecycle,
		fmt.Sprintf("active-attempt:%d", facts.ActiveAttemptID),
		fmt.Sprintf("attempt:%d:%s", facts.AttemptID, facts.AttemptLifecycle),
		runtimeIdentity[0],
		runtimeIdentity[1],
		runtimeIdentity[2],
		runtimeIdentity[3],
		facts.StatusChangedAt,
		facts.StatusChangedFor,
		facts.ReportState,
		fmt.Sprintf("report:%d:%s", facts.ReportOffset, facts.ReportDigest),
		fmt.Sprintf("done-verified:%t", facts.DoneVerified),
		facts.PR,
		fmt.Sprintf("merge:%t:%t", facts.MergeExecuted, facts.MergeAnnounced),
	}
}

func targetKey(kind, id string) string {
	return kind + "\x00" + id
}

func targetID(fleetID, kind, id string) string {
	return "m_" + digest(fleetID, kind, id)
}

func currentnessToken(fleetID, kind, id string, generation []string) CurrentnessToken {
	parts := append([]string{fleetID, kind, id}, generation...)
	return CurrentnessToken{value: "c_" + digest(parts...)}
}

func digest(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = fmt.Fprintf(hash, "%d:", len(part))
		hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func sortedTargetEvidence(items []TargetEvidence) []TargetEvidence {
	items = append([]TargetEvidence(nil), items...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].ID < items[j].ID
	})
	return items
}

func sortedWork(items []WorkEvidence) []WorkEvidence {
	items = append([]WorkEvidence(nil), items...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].ID < items[j].ID
	})
	return items
}

func sortedActionable(items []ActionableEvidence) []ActionableEvidence {
	items = append([]ActionableEvidence(nil), items...)
	sort.Slice(items, func(i, j int) bool {
		left := targetKey(items[i].TargetKind, items[i].TargetID)
		right := targetKey(items[j].TargetKind, items[j].TargetID)
		if left != right {
			return left < right
		}
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].Reason < items[j].Reason
	})
	return items
}

func sortedDeferred(items []DeferredSubject) []DeferredSubject {
	items = append([]DeferredSubject(nil), items...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].TargetID != items[j].TargetID {
			return items[i].TargetID < items[j].TargetID
		}
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].Reason < items[j].Reason
	})
	return items
}

func sortedNextActions(items []NextAction) []NextAction {
	items = append([]NextAction(nil), items...)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		if items[i].Target != items[j].Target {
			return items[i].Target < items[j].Target
		}
		if items[i].Command != items[j].Command {
			return items[i].Command < items[j].Command
		}
		return items[i].Reason < items[j].Reason
	})
	return items
}

func bound(result *SupervisorOrientation) {
	if len(result.Work) > maxOrientationItems {
		result.Omitted += len(result.Work) - maxOrientationItems
		result.Work = result.Work[:maxOrientationItems]
	}
	if len(result.Actionable) > maxOrientationItems {
		result.Omitted += len(result.Actionable) - maxOrientationItems
		result.Actionable = result.Actionable[:maxOrientationItems]
	}
	if len(result.Deferred) > maxOrientationItems {
		result.Omitted += len(result.Deferred) - maxOrientationItems
		result.Deferred = result.Deferred[:maxOrientationItems]
	}
	if len(result.Monitors) > maxOrientationItems {
		result.Omitted += len(result.Monitors) - maxOrientationItems
		result.Monitors = result.Monitors[:maxOrientationItems]
	}
	if len(result.NextActions) > maxOrientationItems {
		result.Omitted += len(result.NextActions) - maxOrientationItems
		result.NextActions = result.NextActions[:maxOrientationItems]
	}
	if len(result.Errors) > maxOrientationItems {
		result.Omitted += len(result.Errors) - maxOrientationItems
		result.Errors = result.Errors[:maxOrientationItems]
	}
	if result.Omitted > 0 {
		result.Truncated = true
	}
	result.FleetID, result.Truncated = boundedText(result.FleetID, result.Truncated)
	for i := range result.Work {
		result.Work[i].ID, result.Truncated = boundedText(result.Work[i].ID, result.Truncated)
		result.Work[i].Kind, result.Truncated = boundedText(result.Work[i].Kind, result.Truncated)
		result.Work[i].State, result.Truncated = boundedText(result.Work[i].State, result.Truncated)
		result.Work[i].Reported, result.Truncated = boundedText(result.Work[i].Reported, result.Truncated)
	}
	for i := range result.Actionable {
		result.Actionable[i].Kind, result.Truncated = boundedText(result.Actionable[i].Kind, result.Truncated)
		result.Actionable[i].Reason, result.Truncated = boundedText(result.Actionable[i].Reason, result.Truncated)
		result.Actionable[i].Provenance, result.Truncated = boundedText(result.Actionable[i].Provenance, result.Truncated)
	}
	for i := range result.NextActions {
		result.NextActions[i].Kind, result.Truncated = boundedText(result.NextActions[i].Kind, result.Truncated)
		result.NextActions[i].Target, result.Truncated = boundedText(result.NextActions[i].Target, result.Truncated)
		result.NextActions[i].Command, result.Truncated = boundedText(result.NextActions[i].Command, result.Truncated)
		result.NextActions[i].Reason, result.Truncated = boundedText(result.NextActions[i].Reason, result.Truncated)
	}
	for i := range result.Errors {
		result.Errors[i].Kind = strings.TrimSpace(result.Errors[i].Kind)
		result.Errors[i].Reason = strings.TrimSpace(result.Errors[i].Reason)
		result.Errors[i].Kind, result.Truncated = boundedText(result.Errors[i].Kind, result.Truncated)
		result.Errors[i].Reason, result.Truncated = boundedText(result.Errors[i].Reason, result.Truncated)
	}
}

func boundedText(value string, truncated bool) (string, bool) {
	runes := []rune(value)
	if len(runes) <= maxOrientationText {
		return value, truncated
	}
	return string(runes[:maxOrientationText]), true
}
