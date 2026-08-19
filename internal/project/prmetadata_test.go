package project

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/ghutil"
)

// Runs a real `gh pr edit` against the fake, exactly as no-mistakes does when it replaces a pull
// request body wholesale: hand's own code is never in this call path, which is the point of the
// test - hand must recover from a write it did not make and could not prevent.
func clobberPR(t *testing.T, url string, args ...string) {
	t.Helper()
	full := append([]string{"pr", "edit", url}, args...)
	cmd := exec.Command("gh", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gh %s: %v: %s", strings.Join(full, " "), err, out)
	}
}

func TestReassertPreservesClosingReferenceAcrossAGateClobber(t *testing.T) {
	home := t.TempDir()
	url := "https://github.com/atqamz/hand/pull/247"
	faketool.GH{PRs: []faketool.GHPR{{
		URL: url, Number: 247, State: "OPEN",
		Body: "Operator-authored summary.\n\nCloses atqamz/hand#247",
	}}}.Install(t, faketool.Bin(t))
	ctx := context.Background()

	if err := ReassertPRMetadata(ctx, home, url); err != nil {
		t.Fatalf("establish: %v", err)
	}
	established, observation := ghutil.FetchPRMetadata(ctx, url)
	if !observation.Found() {
		t.Fatalf("observation = %+v, want found", observation)
	}
	if len(established.ClosingIssuesReferences) != 1 || established.ClosingIssuesReferences[0] != 247 {
		t.Fatalf("closingIssuesReferences after establish = %v, want [247]", established.ClosingIssuesReferences)
	}

	// The gate's replacement body carries no closing reference at all, so a restore that relies on
	// the reference having survived by luck would fail here exactly as it did on the real PRs the
	// issue names.
	clobberPR(t, url, "--body", "## Gate Report\n\nAll checks passed.")

	if err := ReassertPRMetadata(ctx, home, url); err != nil {
		t.Fatalf("reassert after clobber: %v", err)
	}
	after, observation := ghutil.FetchPRMetadata(ctx, url)
	if !observation.Found() {
		t.Fatalf("observation = %+v, want found", observation)
	}
	if len(after.ClosingIssuesReferences) != 1 || after.ClosingIssuesReferences[0] != 247 {
		t.Fatalf("closingIssuesReferences after restore = %v, want [247] restored", after.ClosingIssuesReferences)
	}
	operatorBody, _, ok := ghutil.SplitBody(after.Body)
	if !ok {
		t.Fatal("restored body carries no pipeline region")
	}
	if !strings.Contains(operatorBody, "Closes atqamz/hand#247") {
		t.Fatalf("restored operator body = %q, want the closing reference verbatim", operatorBody)
	}
}

func TestReassertDoesNotChangeDraftOrReadyStateUnlessRequested(t *testing.T) {
	home := t.TempDir()
	url := "https://github.com/atqamz/hand/pull/301"
	faketool.GH{PRs: []faketool.GHPR{{
		URL: url, Number: 301, State: "OPEN", Body: "operator text", Draft: true,
	}}}.Install(t, faketool.Bin(t))
	ctx := context.Background()

	if err := ReassertPRMetadata(ctx, home, url); err != nil {
		t.Fatalf("establish: %v", err)
	}

	// The operator, not the gate, takes the PR out of draft. A run with the region intact must
	// adopt that as the new baseline rather than force it back to draft.
	readyCmd := exec.Command("gh", "pr", "ready", url)
	if out, err := readyCmd.CombinedOutput(); err != nil {
		t.Fatalf("gh pr ready: %v: %s", err, out)
	}
	if err := ReassertPRMetadata(ctx, home, url); err != nil {
		t.Fatalf("reassert after operator marks ready: %v", err)
	}
	meta, _ := ghutil.FetchPRMetadata(ctx, url)
	if meta.Draft {
		t.Fatal("draft = true, want the operator's ready toggle adopted as the new baseline")
	}

	// Now the gate clobbers the body only, never touching draft state. Restoring operator-owned
	// metadata must not silently flip the PR back to draft.
	clobberPR(t, url, "--body", "## Gate Report\n\nripped the body out")
	if err := ReassertPRMetadata(ctx, home, url); err != nil {
		t.Fatalf("reassert after clobber: %v", err)
	}
	meta, _ = ghutil.FetchPRMetadata(ctx, url)
	if meta.Draft {
		t.Fatal("draft = true after restore, want ready state left exactly as the operator set it")
	}
}

func TestReassertDoesNotClearOrReplaceTheAssignee(t *testing.T) {
	home := t.TempDir()
	url := "https://github.com/atqamz/hand/pull/302"
	faketool.GH{PRs: []faketool.GHPR{{
		URL: url, Number: 302, State: "OPEN", Body: "operator text", Assignees: []string{"atqamz"},
	}}}.Install(t, faketool.Bin(t))
	ctx := context.Background()

	if err := ReassertPRMetadata(ctx, home, url); err != nil {
		t.Fatalf("establish: %v", err)
	}

	// A gate destructive enough to replace the body might also touch assignees; the restore must
	// win regardless of what else the clobber carried.
	clobberPR(t, url, "--body", "## Gate Report", "--remove-assignee", "atqamz")

	if err := ReassertPRMetadata(ctx, home, url); err != nil {
		t.Fatalf("reassert after clobber: %v", err)
	}
	meta, _ := ghutil.FetchPRMetadata(ctx, url)
	if len(meta.Assignees) != 1 || meta.Assignees[0] != "atqamz" {
		t.Fatalf("assignees after restore = %v, want [atqamz]", meta.Assignees)
	}
}

func TestReassertPreservesContentOutsideThePipelineRegionVerbatim(t *testing.T) {
	home := t.TempDir()
	url := "https://github.com/atqamz/hand/pull/303"
	operatorBody := "## Summary\n\n- did a thing\n- did another thing\n\n```go\nfmt.Println(\"kept verbatim\")\n```\n\nCloses atqamz/hand#303"
	faketool.GH{PRs: []faketool.GHPR{{URL: url, Number: 303, State: "OPEN", Body: operatorBody}}}.Install(t, faketool.Bin(t))
	ctx := context.Background()

	if err := ReassertPRMetadata(ctx, home, url); err != nil {
		t.Fatalf("establish: %v", err)
	}
	clobberPR(t, url, "--body", "## Gate Report\n\nnothing survives a naive replace")
	if err := ReassertPRMetadata(ctx, home, url); err != nil {
		t.Fatalf("reassert after clobber: %v", err)
	}

	after, _ := ghutil.FetchPRMetadata(ctx, url)
	got, _, ok := ghutil.SplitBody(after.Body)
	if !ok {
		t.Fatal("restored body carries no pipeline region")
	}
	if got != operatorBody {
		t.Fatalf("operator body = %q, want %q verbatim", got, operatorBody)
	}
}

func TestReassertTwoConsecutiveRunsProduceByteIdenticalSnapshot(t *testing.T) {
	home := t.TempDir()
	url := "https://github.com/atqamz/hand/pull/304"
	faketool.GH{PRs: []faketool.GHPR{{
		URL: url, Number: 304, State: "OPEN", Body: "operator text", Assignees: []string{"atqamz"},
	}}}.Install(t, faketool.Bin(t))
	ctx := context.Background()

	if err := ReassertPRMetadata(ctx, home, url); err != nil {
		t.Fatalf("establish: %v", err)
	}
	path, err := prMetadataSnapshotPath(home, url)
	if err != nil {
		t.Fatalf("snapshot path: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}

	if err := ReassertPRMetadata(ctx, home, url); err != nil {
		t.Fatalf("second reassert on an unchanged tree: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("snapshot changed across two runs on an unchanged tree:\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestReassertSanitizesPipelineTextEmojiToSeverityPrefixes(t *testing.T) {
	home := t.TempDir()
	url := "https://github.com/atqamz/hand/pull/305"
	faketool.GH{PRs: []faketool.GHPR{{URL: url, Number: 305, State: "OPEN", Body: "operator text"}}}.Install(t, faketool.Bin(t))
	ctx := context.Background()

	if err := ReassertPRMetadata(ctx, home, url); err != nil {
		t.Fatalf("establish: %v", err)
	}
	clobberPR(t, url, "--body", "<summary>✅ intent - passed</summary>\n⚠️ Medium: risk\n❌ lint failed")
	if err := ReassertPRMetadata(ctx, home, url); err != nil {
		t.Fatalf("reassert after clobber: %v", err)
	}

	after, _ := ghutil.FetchPRMetadata(ctx, url)
	_, pipeline, ok := ghutil.SplitBody(after.Body)
	if !ok {
		t.Fatal("restored body carries no pipeline region")
	}
	for _, want := range []string{"- info:", "- warning:", "- error:"} {
		if !strings.Contains(pipeline, want) {
			t.Fatalf("pipeline region = %q, want it to contain %q", pipeline, want)
		}
	}
	for _, glyph := range []string{"✅", "⚠️", "⚠", "❌"} {
		if strings.Contains(pipeline, glyph) {
			t.Fatalf("pipeline region = %q, still contains emoji glyph %q", pipeline, glyph)
		}
	}
}

func TestReassertEstablishesAPipelineRegionWithoutDestroyingExistingContent(t *testing.T) {
	home := t.TempDir()
	url := "https://github.com/atqamz/hand/pull/306"
	operatorBody := "Written by the operator before hand ever looked at this pull request.\n\nCloses atqamz/hand#306"
	faketool.GH{PRs: []faketool.GHPR{{URL: url, Number: 306, State: "OPEN", Body: operatorBody}}}.Install(t, faketool.Bin(t))
	ctx := context.Background()

	if err := ReassertPRMetadata(ctx, home, url); err != nil {
		t.Fatalf("establish: %v", err)
	}

	after, observation := ghutil.FetchPRMetadata(ctx, url)
	if !observation.Found() {
		t.Fatalf("observation = %+v, want found", observation)
	}
	got, pipeline, ok := ghutil.SplitBody(after.Body)
	if !ok {
		t.Fatal("establish did not leave a pipeline region in place")
	}
	if got != operatorBody {
		t.Fatalf("operator body = %q, want the original content preserved verbatim: %q", got, operatorBody)
	}
	if pipeline != "" {
		t.Fatalf("pipeline region = %q, want it empty on first establishment", pipeline)
	}
	if len(after.ClosingIssuesReferences) != 1 || after.ClosingIssuesReferences[0] != 306 {
		t.Fatalf("closingIssuesReferences = %v, want [306] still resolved from the untouched operator text", after.ClosingIssuesReferences)
	}
}
