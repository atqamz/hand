package project

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/atomicfile"
	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/state"
)

// The operator-owned baseline hand keeps for one pull request, taken whenever a run finds
// hand's own pipeline-region markers intact - what a clobbered body and its fields are
// restored from, not a cache of anything the pipeline itself wrote.
type prMetadataSnapshot struct {
	OperatorBody string   `json:"operatorBody"`
	Assignees    []string `json:"assignees"`
	Draft        bool     `json:"draft"`
	Labels       []string `json:"labels"`
	Milestone    string   `json:"milestone"`
	Reviewers    []string `json:"reviewers"`
}

// ReassertPRMetadata is hand's answer to a gate that treats a pull request body as its own to
// overwrite: it establishes hand's pipeline-owned region on first sight, adopts the operator's
// latest edits while the region survives, and restores the baseline once the region goes missing.
func ReassertPRMetadata(ctx context.Context, homeDir, prURL string) error {
	live, observation := ghutil.FetchPRMetadata(ctx, prURL)
	if !observation.Found() {
		return fmt.Errorf("observe PR %s metadata: %s", prURL, observation.Reason())
	}

	snap, existed, err := loadPRMetadataSnapshot(homeDir, prURL)
	if err != nil {
		return err
	}

	operator, _, hasMarkers := ghutil.SplitBody(live.Body)

	if !existed {
		return establishPRMetadata(ctx, homeDir, prURL, live, operator, hasMarkers)
	}
	if hasMarkers {
		return adoptPRMetadata(homeDir, prURL, live, operator, snap)
	}
	return restorePRMetadataSnapshot(ctx, homeDir, prURL, live, snap)
}

// Runs the first time hand ever looks at a pull request: a region already present is adopted
// as-is, otherwise hand composes one around whatever body is already there and verifies the
// write before trusting it as the new baseline.
func establishPRMetadata(ctx context.Context, homeDir, prURL string, live ghutil.PRMetadata, operator string, hasMarkers bool) error {
	if hasMarkers {
		return saveSnapshotFrom(homeDir, prURL, live, operator)
	}

	operatorBody := strings.TrimRight(live.Body, "\n")
	if err := ghutil.SetPRBody(ctx, prURL, ghutil.ComposeBody(operatorBody, "")); err != nil {
		return err
	}
	after, observation := ghutil.FetchPRMetadata(ctx, prURL)
	if !observation.Found() {
		return fmt.Errorf("verify pipeline region establishment for %s: %s", prURL, observation.Reason())
	}
	gotOperator, _, ok := ghutil.SplitBody(after.Body)
	if !ok || gotOperator != operatorBody {
		return fmt.Errorf("pipeline region establishment for %s did not verify", prURL)
	}
	return saveSnapshotFrom(homeDir, prURL, live, operatorBody)
}

// Runs when the region is intact: nothing has clobbered this pull request since hand last
// looked, so the operator's current edits become the new baseline. An unchanged snapshot is
// left unwritten, which keeps two consecutive runs on an unchanged tree idempotent.
func adoptPRMetadata(homeDir, prURL string, live ghutil.PRMetadata, operator string, snap prMetadataSnapshot) error {
	next := prMetadataSnapshot{
		OperatorBody: operator,
		Assignees:    live.Assignees,
		Draft:        live.Draft,
		Labels:       live.Labels,
		Milestone:    live.Milestone,
		Reviewers:    live.Reviewers,
	}
	if snapshotsEqual(next, snap) {
		return nil
	}
	return savePRMetadataSnapshot(homeDir, prURL, next)
}

// Runs when the region is missing but a snapshot exists: the only way hand's own markers
// disappear is a full-body replacement, so the live body is sanitized and wrapped back around
// the last known-good operator body and fields, then verified unconditionally - a mismatch is a hard error, never a best-effort warning.
func restorePRMetadataSnapshot(ctx context.Context, homeDir, prURL string, live ghutil.PRMetadata, snap prMetadataSnapshot) error {
	restoredBody := ghutil.ComposeBody(snap.OperatorBody, ghutil.SanitizePipelineText(live.Body))
	want := ghutil.PRMetadata{
		Body:      restoredBody,
		Assignees: snap.Assignees,
		Draft:     snap.Draft,
		Labels:    snap.Labels,
		Milestone: snap.Milestone,
		Reviewers: snap.Reviewers,
	}
	if err := ghutil.RestorePRMetadata(ctx, prURL, live, want); err != nil {
		return fmt.Errorf("restore operator-owned metadata for %s: %w", prURL, err)
	}

	after, observation := ghutil.FetchPRMetadata(ctx, prURL)
	if !observation.Found() {
		return fmt.Errorf("verify restored metadata for %s: %s", prURL, observation.Reason())
	}
	gotOperator, _, ok := ghutil.SplitBody(after.Body)
	if !ok || gotOperator != snap.OperatorBody {
		return fmt.Errorf("restored PR metadata for %s did not verify: operator-owned body content mismatch", prURL)
	}
	if !equalMetadataFields(after, snap) {
		return fmt.Errorf("restored PR metadata for %s did not verify: assignee, draft, label, milestone or reviewer state mismatch", prURL)
	}
	return savePRMetadataSnapshot(homeDir, prURL, snap)
}

func saveSnapshotFrom(homeDir, prURL string, live ghutil.PRMetadata, operatorBody string) error {
	return savePRMetadataSnapshot(homeDir, prURL, prMetadataSnapshot{
		OperatorBody: operatorBody,
		Assignees:    live.Assignees,
		Draft:        live.Draft,
		Labels:       live.Labels,
		Milestone:    live.Milestone,
		Reviewers:    live.Reviewers,
	})
}

func snapshotsEqual(a, b prMetadataSnapshot) bool {
	return a.OperatorBody == b.OperatorBody && a.Draft == b.Draft && a.Milestone == b.Milestone &&
		equalStringSets(a.Assignees, b.Assignees) && equalStringSets(a.Labels, b.Labels) && equalStringSets(a.Reviewers, b.Reviewers)
}

func equalMetadataFields(m ghutil.PRMetadata, snap prMetadataSnapshot) bool {
	return m.Draft == snap.Draft && m.Milestone == snap.Milestone &&
		equalStringSets(m.Assignees, snap.Assignees) && equalStringSets(m.Labels, snap.Labels) && equalStringSets(m.Reviewers, snap.Reviewers)
}

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, v := range a {
		counts[v]++
	}
	for _, v := range b {
		counts[v]--
	}
	for _, c := range counts {
		if c != 0 {
			return false
		}
	}
	return true
}

func prMetadataSnapshotPath(homeDir, prURL string) (string, error) {
	if !state.ValidatePRURL(prURL) {
		return "", fmt.Errorf("invalid PR URL %q", prURL)
	}
	name := strings.ReplaceAll(strings.TrimPrefix(prURL, "https://github.com/"), "/", "-")
	return filepath.Join(homeDir, "data", "pr-metadata", name+".json"), nil
}

func loadPRMetadataSnapshot(homeDir, prURL string) (prMetadataSnapshot, bool, error) {
	path, err := prMetadataSnapshotPath(homeDir, prURL)
	if err != nil {
		return prMetadataSnapshot{}, false, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return prMetadataSnapshot{}, false, nil
	}
	if err != nil {
		return prMetadataSnapshot{}, false, fmt.Errorf("read PR metadata snapshot for %s: %w", prURL, err)
	}
	var snap prMetadataSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return prMetadataSnapshot{}, false, fmt.Errorf("decode PR metadata snapshot for %s: %w", prURL, err)
	}
	return snap, true, nil
}

func savePRMetadataSnapshot(homeDir, prURL string, snap prMetadataSnapshot) error {
	path, err := prMetadataSnapshotPath(homeDir, prURL)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create PR metadata directory: %w", err)
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("encode PR metadata snapshot for %s: %w", prURL, err)
	}
	return atomicfile.Write(path, ".prmetadata-*", data, 0o644)
}
