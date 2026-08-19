package ghutil

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
)

func TestComposeSplitBodyRoundTrips(t *testing.T) {
	operator := "Operator wrote this.\n\nCloses atqamz/hand#247"
	composed := ComposeBody(operator, "gate report line one\ngate report line two")
	gotOperator, gotPipeline, ok := SplitBody(composed)
	if !ok {
		t.Fatalf("SplitBody(%q) = ok false, want a region found", composed)
	}
	if gotOperator != operator {
		t.Fatalf("operator body = %q, want %q", gotOperator, operator)
	}
	if gotPipeline != "gate report line one\ngate report line two" {
		t.Fatalf("pipeline body = %q, want the composed pipeline text back", gotPipeline)
	}

	// Composing again from the split halves must reproduce the same bytes: this is what keeps a
	// second, unchanged run from writing anything at all.
	if again := ComposeBody(gotOperator, gotPipeline); again != composed {
		t.Fatalf("ComposeBody is not idempotent over its own SplitBody: got %q, want %q", again, composed)
	}
}

func TestSplitBodyReportsNoRegionWhenMarkersAreMissing(t *testing.T) {
	if _, _, ok := SplitBody("plain operator text with no markers at all"); ok {
		t.Fatal("SplitBody found a region in text that carries no markers")
	}
	if _, _, ok := SplitBody(PipelineRegionStart + " without an end marker"); ok {
		t.Fatal("SplitBody found a region with a start marker but no end marker")
	}
}

func TestSanitizePipelineTextMapsKnownGlyphsAndStripsTheRest(t *testing.T) {
	in := "<summary>✅ **intent** - passed</summary>\n⚠️ Medium: risk\n❌ lint failed\n🚀 shipped it"
	out := SanitizePipelineText(in)
	for _, want := range []string{"- info:", "- warning:", "- error:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("sanitized text = %q, want it to contain %q", out, want)
		}
	}
	for _, glyph := range []string{"✅", "⚠️", "⚠", "❌", "🚀"} {
		if strings.Contains(out, glyph) {
			t.Fatalf("sanitized text = %q, still contains emoji glyph %q", out, glyph)
		}
	}
}

func TestFetchPRMetadataDecodesEveryField(t *testing.T) {
	bin := faketool.Bin(t)
	pr := faketool.GHPR{
		URL:       "https://github.com/atqamz/hand/pull/247",
		Number:    247,
		State:     "OPEN",
		Body:      "operator text\n\nCloses atqamz/hand#247",
		Assignees: []string{"atqamz"},
		Draft:     true,
		Labels:    []string{"bug"},
		Milestone: "v1",
		Reviewers: []string{"reviewer1"},
	}
	faketool.GH{PRs: []faketool.GHPR{pr}}.Install(t, bin)

	meta, observation := FetchPRMetadata(context.Background(), pr.URL)
	if !observation.Found() {
		t.Fatalf("observation = %+v, want the fake PR found", observation)
	}
	if meta.Body != pr.Body {
		t.Fatalf("body = %q, want %q", meta.Body, pr.Body)
	}
	if !meta.Draft {
		t.Fatal("draft = false, want true")
	}
	if meta.State != "OPEN" {
		t.Fatalf("state = %q, want OPEN", meta.State)
	}
	if got := sortedCopy(meta.Assignees); !equalSorted(got, []string{"atqamz"}) {
		t.Fatalf("assignees = %v, want [atqamz]", got)
	}
	if got := sortedCopy(meta.Labels); !equalSorted(got, []string{"bug"}) {
		t.Fatalf("labels = %v, want [bug]", got)
	}
	if meta.Milestone != "v1" {
		t.Fatalf("milestone = %q, want v1", meta.Milestone)
	}
	if got := sortedCopy(meta.Reviewers); !equalSorted(got, []string{"reviewer1"}) {
		t.Fatalf("reviewers = %v, want [reviewer1]", got)
	}
	if len(meta.ClosingIssuesReferences) != 1 || meta.ClosingIssuesReferences[0] != 247 {
		t.Fatalf("closingIssuesReferences = %v, want [247]", meta.ClosingIssuesReferences)
	}
}

func TestRestorePRMetadataIssuesOnlyTheChangedFields(t *testing.T) {
	bin := faketool.Bin(t)
	log := filepath.Join(t.TempDir(), "gh.log")
	pr := faketool.GHPR{
		URL:       "https://github.com/atqamz/hand/pull/301",
		Number:    301,
		State:     "OPEN",
		Body:      "clobbered report",
		Assignees: []string{"atqamz"},
		Draft:     false,
		Labels:    []string{"bug"},
		Milestone: "v1",
		Reviewers: []string{"reviewer1"},
	}
	faketool.GH{PRs: []faketool.GHPR{pr}, Log: log}.Install(t, bin)

	live, observation := FetchPRMetadata(context.Background(), pr.URL)
	if !observation.Found() {
		t.Fatalf("observation = %+v, want the fake PR found", observation)
	}
	want := PRMetadata{
		Body:      "restored operator body",
		Assignees: []string{"atqamz", "operator2"},
		Draft:     true,
		Labels:    []string{"bug", "priority"},
		Milestone: "",
		Reviewers: []string{"reviewer2"},
	}
	if err := RestorePRMetadata(context.Background(), pr.URL, live, want); err != nil {
		t.Fatalf("RestorePRMetadata: %v", err)
	}

	after, observation := FetchPRMetadata(context.Background(), pr.URL)
	if !observation.Found() {
		t.Fatalf("observation = %+v, want the fake PR found after restore", observation)
	}
	if after.Body != want.Body {
		t.Fatalf("body = %q, want %q", after.Body, want.Body)
	}
	if !after.Draft {
		t.Fatal("draft = false, want true after restore")
	}
	if got := sortedCopy(after.Assignees); !equalSorted(got, sortedCopy(want.Assignees)) {
		t.Fatalf("assignees = %v, want %v", got, want.Assignees)
	}
	if got := sortedCopy(after.Labels); !equalSorted(got, sortedCopy(want.Labels)) {
		t.Fatalf("labels = %v, want %v", got, want.Labels)
	}
	if after.Milestone != "" {
		t.Fatalf("milestone = %q, want it removed", after.Milestone)
	}
	if got := sortedCopy(after.Reviewers); !equalSorted(got, sortedCopy(want.Reviewers)) {
		t.Fatalf("reviewers = %v, want %v", got, want.Reviewers)
	}

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("read gh invocation log: %v", err)
	}
	if strings.Count(string(data), "pr ready") != 1 {
		t.Fatalf("gh invocation log = %q, want exactly one pr ready call", data)
	}
}

func TestRestorePRMetadataLeavesDraftAloneWhenNothingChanged(t *testing.T) {
	bin := faketool.Bin(t)
	log := filepath.Join(t.TempDir(), "gh.log")
	pr := faketool.GHPR{
		URL:   "https://github.com/atqamz/hand/pull/302",
		State: "OPEN",
		Body:  "unchanged body",
		Draft: true,
	}
	faketool.GH{PRs: []faketool.GHPR{pr}, Log: log}.Install(t, bin)

	live, _ := FetchPRMetadata(context.Background(), pr.URL)
	want := live
	if err := RestorePRMetadata(context.Background(), pr.URL, live, want); err != nil {
		t.Fatalf("RestorePRMetadata: %v", err)
	}

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("read gh invocation log: %v", err)
	}
	if strings.Contains(string(data), "pr edit") || strings.Contains(string(data), "pr ready") {
		t.Fatalf("gh invocation log = %q, want no write when nothing differs", data)
	}
}

func TestRestorePRMetadataSkipsDraftToggleOnceAPRIsNotOpen(t *testing.T) {
	bin := faketool.Bin(t)
	log := filepath.Join(t.TempDir(), "gh.log")
	pr := faketool.GHPR{
		URL:   "https://github.com/atqamz/hand/pull/303",
		State: "MERGED",
		Body:  "clobbered",
		Draft: false,
	}
	faketool.GH{PRs: []faketool.GHPR{pr}, Log: log}.Install(t, bin)

	live, _ := FetchPRMetadata(context.Background(), pr.URL)
	want := live
	want.Draft = true
	if err := RestorePRMetadata(context.Background(), pr.URL, live, want); err != nil {
		t.Fatalf("RestorePRMetadata: %v", err)
	}

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("read gh invocation log: %v", err)
	}
	if strings.Contains(string(data), "pr ready") {
		t.Fatalf("gh invocation log = %q, want no pr ready call against a merged pull request", data)
	}
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func equalSorted(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
