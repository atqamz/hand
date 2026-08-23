// Package supervision owns Supervisor turn delivery: exact wake episode
// identity, the disposable wake/progress ledger, the provider-neutral wait,
// and managed host-integration installation. It deliberately does not live in
// internal/harness, which means Worker Harness launch/routing only.
package supervision

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/atqamz/hand/internal/orientation"
)

// Episode is one actionable subject at one exact currentness. The zero value
// is never a valid episode: every field is required for the key.
type Episode struct {
	FleetID     string
	TargetID    string
	TargetKind  string
	Currentness orientation.CurrentnessToken
	Kind        string
}

// Key is the stable dedupe identity: fleet, target, currentness token, and
// actionable kind. Rendered reason, PIDs, wall-clock, display labels, and
// conversation IDs are deliberately excluded from deciding a wake.
func (e Episode) Key() string {
	parts := []string{e.FleetID, e.TargetKind, e.TargetID, e.Currentness.String(), e.Kind}
	hash := sha256.New()
	for _, part := range parts {
		_, _ = fmt.Fprintf(hash, "%d:", len(part))
		hash.Write([]byte(part))
	}
	return "w_" + hex.EncodeToString(hash.Sum(nil))
}

// FromEvidence derives wake episodes from unbounded underlying evidence,
// never the bounded rendered orientation: a new subject behind a full display
// slice is exactly the starvation this seam prevents. Duplicates coalesce.
func FromEvidence(evidence orientation.Evidence) []Episode {
	seen := make(map[string]bool, len(evidence.Actionable))
	var episodes []Episode
	for _, item := range evidence.Actionable {
		target := orientation.TargetFor(evidence.FleetID, orientation.TargetEvidence{
			ID:         item.TargetID,
			Kind:       item.TargetKind,
			Generation: item.Generation,
		})
		if target.ID == "" || target.Currentness.IsZero() {
			continue
		}
		episode := Episode{
			FleetID:     evidence.FleetID,
			TargetID:    item.TargetID,
			TargetKind:  item.TargetKind,
			Currentness: target.Currentness,
			Kind:        item.Kind,
		}
		key := episode.Key()
		if seen[key] {
			continue
		}
		seen[key] = true
		episodes = append(episodes, episode)
	}
	sort.Slice(episodes, func(i, j int) bool { return episodes[i].Key() < episodes[j].Key() })
	return episodes
}

// Keys maps episodes to their dedupe identities in episode order.
func Keys(episodes []Episode) []string {
	keys := make([]string, len(episodes))
	for i, episode := range episodes {
		keys[i] = episode.Key()
	}
	return keys
}

const maxWakeReason = 240

// WakeText is the bounded mechanism input handed to a host bridge. It carries
// no worker report prose, plan brief, pane content, or environment: a Hand
// wake is mechanism input, never operator authority or mutation authorization.
func WakeText(fleetID string) string {
	text := fmt.Sprintf("Hand has current actionable work for Fleet %s. Run `hand orient` before reasoning or acting.", fleetID)
	if runes := []rune(text); len(runes) > maxWakeReason {
		text = string(runes[:maxWakeReason])
	}
	return text
}
