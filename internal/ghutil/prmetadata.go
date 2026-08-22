package ghutil

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/atqamz/hand/internal/integration"
)

// PRMetadata is everything about a pull request that an operator, not a delivery pipeline, owns:
// body text outside the pipeline's own region, draft/ready state, assignees, labels, milestone
// and reviewer requests. State and ClosingIssuesReferences are read-only context for a caller.
type PRMetadata struct {
	Body                    string
	Assignees               []string
	Draft                   bool
	Labels                  []string
	Milestone               string
	Reviewers               []string
	State                   string
	ClosingIssuesReferences []int
}

const prMetadataFields = "body,assignees,isDraft,labels,milestone,reviewRequests,closingIssuesReferences,state"

// FetchPRMetadata reads the metadata gate runs and operator edits both write to, in one query so
// a caller reasons about a single consistent snapshot rather than several queries racing a
// concurrent write.
func FetchPRMetadata(ctx context.Context, pr string) (PRMetadata, PRObservation) {
	args := []string{"pr", "view", pr, "--json", prMetadataFields}
	probe := Probe{Command: ghCommand(args)}
	var payload struct {
		Body      string `json:"body"`
		Assignees []struct {
			Login string `json:"login"`
		} `json:"assignees"`
		IsDraft bool `json:"isDraft"`
		Labels  []struct {
			Name string `json:"name"`
		} `json:"labels"`
		Milestone *struct {
			Title string `json:"title"`
		} `json:"milestone"`
		ReviewRequests []struct {
			Login string `json:"login"`
			Name  string `json:"name"`
		} `json:"reviewRequests"`
		ClosingIssuesReferences []struct {
			Number int `json:"number"`
		} `json:"closingIssuesReferences"`
		State string `json:"state"`
	}
	if terminal := decodeGHPayload(ctx, probe, &payload, args...); terminal != nil {
		return PRMetadata{}, *terminal
	}

	meta := PRMetadata{Body: payload.Body, Draft: payload.IsDraft, State: payload.State}
	for _, a := range payload.Assignees {
		meta.Assignees = append(meta.Assignees, a.Login)
	}
	for _, l := range payload.Labels {
		meta.Labels = append(meta.Labels, l.Name)
	}
	if payload.Milestone != nil {
		meta.Milestone = payload.Milestone.Title
	}
	for _, r := range payload.ReviewRequests {
		name := r.Login
		if name == "" {
			name = r.Name
		}
		meta.Reviewers = append(meta.Reviewers, name)
	}
	for _, c := range payload.ClosingIssuesReferences {
		meta.ClosingIssuesReferences = append(meta.ClosingIssuesReferences, c.Number)
	}
	return meta, PRObservation{State: ObservationFound, URL: pr, Probe: probe}
}

// SetPRBody replaces a pull request's body outright, for establishing the pipeline-owned region
// the first time hand observes a pull request that does not carry one yet.
func SetPRBody(ctx context.Context, pr, body string) error {
	if err := runGH(ctx, "pr", "edit", pr, "--body", body); err != nil {
		return fmt.Errorf("set PR body for %s: %w", pr, err)
	}
	return nil
}

// RestorePRMetadata pushes want's fields to GitHub wherever they differ from live: one pr edit
// carrying every add/remove flag that changed, then a pr ready toggle only if draft state
// differs. Draft is left alone once a pull request is no longer open, since gh pr ready refuses.
func RestorePRMetadata(ctx context.Context, pr string, live, want PRMetadata) error {
	args := []string{"pr", "edit", pr}
	changed := false

	if live.Body != want.Body {
		args = append(args, "--body", want.Body)
		changed = true
	}
	addAssignees, removeAssignees := stringSetDiff(want.Assignees, live.Assignees)
	for _, a := range addAssignees {
		args = append(args, "--add-assignee", a)
		changed = true
	}
	for _, a := range removeAssignees {
		args = append(args, "--remove-assignee", a)
		changed = true
	}
	addLabels, removeLabels := stringSetDiff(want.Labels, live.Labels)
	for _, l := range addLabels {
		args = append(args, "--add-label", l)
		changed = true
	}
	for _, l := range removeLabels {
		args = append(args, "--remove-label", l)
		changed = true
	}
	if want.Milestone != live.Milestone {
		if want.Milestone == "" {
			args = append(args, "--remove-milestone")
		} else {
			args = append(args, "--milestone", want.Milestone)
		}
		changed = true
	}
	addReviewers, removeReviewers := stringSetDiff(want.Reviewers, live.Reviewers)
	for _, r := range addReviewers {
		args = append(args, "--add-reviewer", r)
		changed = true
	}
	for _, r := range removeReviewers {
		args = append(args, "--remove-reviewer", r)
		changed = true
	}

	if changed {
		if err := runGH(ctx, args...); err != nil {
			return fmt.Errorf("restore PR metadata for %s: %w", pr, err)
		}
	}
	if want.Draft != live.Draft && live.State == "OPEN" {
		readyArgs := []string{"pr", "ready", pr}
		if want.Draft {
			readyArgs = append(readyArgs, "--undo")
		}
		if err := runGH(ctx, readyArgs...); err != nil {
			return fmt.Errorf("restore PR draft state for %s: %w", pr, err)
		}
	}
	return nil
}

func runGH(ctx context.Context, args ...string) error {
	_, stderr, err := integration.Run(ctx, "github/gh", "", args...)
	if err != nil {
		return fmt.Errorf("gh %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(stderr)))
	}
	return nil
}

func stringSetDiff(want, live []string) (add, remove []string) {
	liveSet := make(map[string]bool, len(live))
	for _, v := range live {
		liveSet[v] = true
	}
	wantSet := make(map[string]bool, len(want))
	for _, v := range want {
		wantSet[v] = true
	}
	for _, v := range want {
		if !liveSet[v] {
			add = append(add, v)
		}
	}
	for _, v := range live {
		if !wantSet[v] {
			remove = append(remove, v)
		}
	}
	return add, remove
}

// PipelineRegionStart and PipelineRegionEnd delimit the only part of a pull request body a
// pipeline may rewrite. Hand alone reads and writes these markers, so their presence is the
// signal that no full-body replacement has happened since hand last composed the body.
const (
	PipelineRegionStart = "<!-- hand:pipeline-region:start -->"
	PipelineRegionEnd   = "<!-- hand:pipeline-region:end -->"
)

// ComposeBody joins operator-owned text with the pipeline's own region. Round-tripped through
// SplitBody it reproduces operatorBody exactly, which is what keeps two consecutive, unchanged
// runs from rewriting a body neither call needed to touch.
func ComposeBody(operatorBody, pipelineBody string) string {
	operatorBody = strings.TrimRight(operatorBody, "\n")
	pipelineBody = strings.TrimSpace(pipelineBody)
	return operatorBody + "\n\n" + PipelineRegionStart + "\n" + pipelineBody + "\n" + PipelineRegionEnd
}

// SplitBody reports the operator-owned text surrounding the pipeline region and the pipeline
// region's own content. ok is false when body carries no complete region, which is the signal a
// caller uses to detect a full-body replacement that erased it.
func SplitBody(body string) (operatorBody, pipelineBody string, ok bool) {
	start := strings.Index(body, PipelineRegionStart)
	end := strings.Index(body, PipelineRegionEnd)
	if start < 0 || end < 0 || end < start {
		return "", "", false
	}
	before := strings.Trim(body[:start], "\n")
	after := strings.Trim(body[end+len(PipelineRegionEnd):], "\n")
	operatorBody = before
	if after != "" {
		operatorBody = before + "\n\n" + after
	}
	return operatorBody, strings.TrimSpace(body[start+len(PipelineRegionStart) : end]), true
}

var emojiSeverity = []struct {
	glyph  string
	prefix string
}{
	{"✅", "- info:"},
	{"⚠️", "- warning:"},
	{"⚠", "- warning:"},
	{"❌", "- error:"},
}

// Strips any glyph emojiSeverity does not name a severity for, so "no emoji" holds
// unconditionally rather than only for the cases this package anticipated.
var residualEmoji = regexp.MustCompile(`[\x{1F300}-\x{1FAFF}\x{2600}-\x{27BF}\x{FE0F}]`)

// SanitizePipelineText maps the gate's check-mark and warning glyphs to this repository's own
// severity prefixes and removes every other emoji, so a pipeline-authored region never reintroduces
// what AGENTS.md forbids even when the gate's own output does.
func SanitizePipelineText(s string) string {
	for _, e := range emojiSeverity {
		s = strings.ReplaceAll(s, e.glyph, e.prefix)
	}
	return residualEmoji.ReplaceAllString(s, "")
}
