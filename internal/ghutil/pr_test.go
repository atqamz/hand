package ghutil

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/faketool"
)

func requireUnknown(t *testing.T, o PRObservation) {
	t.Helper()
	if o.Found() || o.Absent() {
		t.Fatalf("observation = %+v, want unknown rather than a finding", o)
	}
	if !o.Unknown() {
		t.Fatalf("observation = %+v, want Unknown() to report the state it is in", o)
	}
}

// Fakes `gh pr view --json state`, emitting a stderr line ahead of the JSON payload so a
// CombinedOutput regression at the call site fails the parse the same way real gh's progress
// output does.
func writeFakeGHPRView(t *testing.T, state string, exitCode int, stderrLine string) {
	t.Helper()
	bin := faketool.Bin(t)
	response := faketool.GHResponse{
		Command: "pr view",
		Stderr:  stderrLine,
		Exit:    exitCode,
	}
	if exitCode == 0 {
		response.Stdout = "{\"state\":\"" + state + "\"}\n"
	}
	faketool.GH{Responses: []faketool.GHResponse{response}}.Install(t, bin)
}

// The absence-proving diagnostic quoted from real gh, which tests/contract rechecks against both
// the shared fake and the installed tool.
const notFoundDiagnostic = "GraphQL: Could not resolve to a PullRequest with the number of 42. (repository.pullRequest)\n"

// Nothing built from a zero value or an unrecognized state may read as an absence: that is what
// makes an absence impossible to reach by accident rather than by a check someone added.
func TestOnlyThePositiveStatesAreFindings(t *testing.T) {
	for _, o := range []PRObservation{{}, {State: ObservationUnknown}, {State: ObservationState("")}, {State: ObservationState("mislaid")}} {
		requireUnknown(t, o)
	}
	if !(PRObservation{State: ObservationAbsent}).Absent() {
		t.Fatal("want an explicitly absent observation to report itself absent")
	}
	if !(PRObservation{State: ObservationFound}).Found() {
		t.Fatal("want an explicitly found observation to report itself found")
	}
}

func TestObserveMergeStateIgnoresStderrNoise(t *testing.T) {
	for _, c := range []struct {
		state string
		want  bool
	}{{"MERGED", true}, {"OPEN", false}} {
		writeFakeGHPRView(t, c.state, 0, "Warning: gh version 2.40.0 is out of date")
		got := ObserveMergeState(context.Background(), "42")
		if !got.Found() {
			t.Fatalf("ObserveMergeState with state %s = %+v, want the PR found", c.state, got)
		}
		if got.Merged != c.want {
			t.Errorf("ObserveMergeState with state %s reported merged=%v, want %v", c.state, got.Merged, c.want)
		}
		if got.URL != "42" {
			t.Errorf("URL = %q, want the observed PR named", got.URL)
		}
	}
}

// Absence is a positive finding, so it is read off the one diagnostic that proves it and nothing
// else. Its reason must not read as a failure, because callers act on it.
func TestObserveMergeStateReportsAbsenceForTheNotFoundDiagnostic(t *testing.T) {
	faketool.GH{Responses: []faketool.GHResponse{{Command: "pr view", Stderr: notFoundDiagnostic, Exit: 1}}}.Install(t, faketool.Bin(t))
	got := ObserveMergeState(context.Background(), "42")
	if !got.Absent() {
		t.Fatalf("observation = %+v, want absent for the diagnostic that proves the PR is not there", got)
	}
	if got.Unknown() || got.Found() {
		t.Fatalf("observation = %+v, want absent to be neither unknown nor found", got)
	}
	if !strings.Contains(got.Reason(), "no such pull request") {
		t.Fatalf("Reason() = %q, want it to report the absence rather than a failure", got.Reason())
	}
}

// Every way a query can fail to complete, each one asserted unknown for every entry point. An
// absence here would license teardown to release work that may not have landed (atqamz/hand#241).
func TestNoFailedQueryIsEverAbsent(t *testing.T) {
	for _, c := range []struct {
		name   string
		stdout string
		stderr string
		exit   int
	}{
		{name: "network failure", stderr: "dial tcp 140.82.121.6:443: connect: network is unreachable\n", exit: 1},
		{name: "dns failure", stderr: "dial tcp: lookup api.github.com: no such host\n", exit: 1},
		{name: "request timeout", stderr: "Post \"https://api.github.com/graphql\": context deadline exceeded (Client.Timeout exceeded while awaiting headers)\n", exit: 1},
		{name: "authentication failure", stderr: "gh: HTTP 401: Bad credentials (https://api.github.com/graphql)\n", exit: 1},
		{name: "authorization failure", stderr: "gh: HTTP 403: Resource not accessible by integration (https://api.github.com/graphql)\n", exit: 1},
		{name: "unreadable repository", stderr: "GraphQL: Could not resolve to a Repository with the name of 'owner/repo'. (repository)\n", exit: 1},
		{name: "rate limited", stderr: "GraphQL: API rate limit exceeded for user ID 1. (rateLimit)\n", exit: 1},
		{name: "exit non-zero with no diagnostic at all", exit: 1},
		{name: "truncated payload at exit zero", stdout: "{\"state\":\"MER\n", exit: 0},
		{name: "empty payload at exit zero", stdout: "", exit: 0},
		{name: "a proxy error page at exit zero", stdout: "<html><title>502 Bad Gateway</title></html>\n", exit: 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			faketool.GH{Responses: []faketool.GHResponse{
				{Command: "pr view", Stdout: c.stdout, Stderr: c.stderr, Exit: c.exit},
				{Command: "pr list", Stdout: c.stdout, Stderr: c.stderr, Exit: c.exit},
			}}.Install(t, faketool.Bin(t))
			requireUnknown(t, ObserveMergeState(context.Background(), "42"))
			requireUnknown(t, ObserveHeadCommit(context.Background(), "42"))
			requireUnknown(t, ObservePRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"}))
		})
	}
}

// An answer gh reports at exit zero without the field the query asked for is unknown too: an empty
// string is not a merge state, and treating it as one would read as "not merged".
func TestObserveMergeStateIsUnknownWithoutAState(t *testing.T) {
	faketool.GH{Responses: []faketool.GHResponse{{Command: "pr view", Stdout: "{}\n"}}}.Install(t, faketool.Bin(t))
	requireUnknown(t, ObserveMergeState(context.Background(), "42"))
}

func TestPRObservationIsUnknownWhenTheContextIsAlreadyCancelled(t *testing.T) {
	writeFakeGHPRView(t, "MERGED", 0, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	requireUnknown(t, ObserveMergeState(ctx, "42"))
	requireUnknown(t, ObserveHeadCommit(ctx, "42"))
	requireUnknown(t, ObservePRByBranch(ctx, "task-1-branch", PRSearchTarget{Repo: "owner/repo"}))
}

// A gh that never answers and is killed by the caller's deadline is the case a bare exit-code check
// gets wrong: the subprocess dies, gh prints nothing, and nothing has been observed.
func TestPRObservationIsUnknownWhenGhIsKilledByTheDeadline(t *testing.T) {
	faketool.GH{Hang: []string{"pr view", "pr list"}}.Install(t, faketool.Bin(t))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	requireUnknown(t, ObserveMergeState(ctx, "42"))
	requireUnknown(t, ObservePRByBranch(ctx, "task-1-branch", PRSearchTarget{Repo: "owner/repo"}))
}

// An unknown has to be reportable: a caller refusing to act names the command that answered
// nothing, so an operator can run it themselves rather than guess what hand tried.
func TestUnknownNamesTheCommandThatWasRun(t *testing.T) {
	faketool.GH{Responses: []faketool.GHResponse{
		{Command: "pr view", Stderr: "gh: HTTP 401: Bad credentials\n", Exit: 1},
		{Command: "pr list", Stderr: "gh: HTTP 401: Bad credentials\n", Exit: 1},
	}}.Install(t, faketool.Bin(t))
	merge := ObserveMergeState(context.Background(), "42")
	if !strings.Contains(merge.Reason(), "gh pr view 42 --json state") || !strings.Contains(merge.Reason(), "Bad credentials") {
		t.Fatalf("Reason() = %q, want the command and what it answered", merge.Reason())
	}
	byBranch := ObservePRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"})
	if !strings.Contains(byBranch.Reason(), "gh pr list --repo owner/repo --head task-1-branch") {
		t.Fatalf("Reason() = %q, want the list command named", byBranch.Reason())
	}
}

// The shim internal/watcher still calls has to fail closed for as long as it exists: a bare bool is
// exactly what let a failed query read as an unmerged PR.
func TestPRIsMergedErrorsRatherThanReportingNotMerged(t *testing.T) {
	writeFakeGHPRView(t, "", 1, "")
	merged, err := PRIsMerged(context.Background(), "42")
	if err == nil {
		t.Fatal("want an error when gh exits non-zero rather than a false merge state")
	}
	if merged {
		t.Fatal("want merged=false alongside the error")
	}
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("got %q, want the exit status in the message", err)
	}
}

// Fakes `gh pr list --json number,url,state,headRepository`, emitting a stderr line ahead of the
// JSON array payload for the same reason writeFakeGHPRView does: a CombinedOutput regression at
// the call site must fail the parse.
func writeFakeGHPRList(t *testing.T, body string, exitCode int, stderrLine string) {
	t.Helper()
	bin := faketool.Bin(t)
	faketool.GH{Responses: []faketool.GHResponse{{
		Command: "pr list", Stdout: body, Stderr: stderrLine, Exit: exitCode,
	}}}.Install(t, bin)
}

func TestObservePRByBranchReturnsMatch(t *testing.T) {
	writeFakeGHPRList(t, `[{"url":"https://github.com/owner/repo/pull/5","state":"MERGED"}]`, 0, "Warning: gh version 2.40.0 is out of date")
	got := ObservePRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"})
	if !got.Found() || got.URL != "https://github.com/owner/repo/pull/5" || !got.Merged {
		t.Fatalf("observation = %+v, want the merged PR found", got)
	}
}

// The atqamz/hand#77 regression: a branch carrying a merged PR alongside a closed-unmerged
// one (a duplicate opened by mistake, say) must resolve to the merged PR rather than an arbitrary
// pick.
func TestObservePRByBranchPrefersMergedOverClosedUnmerged(t *testing.T) {
	writeFakeGHPRList(t, `[{"number":9,"url":"https://github.com/owner/repo/pull/9","state":"CLOSED"},`+
		`{"number":5,"url":"https://github.com/owner/repo/pull/5","state":"MERGED"}]`, 0, "")
	got := ObservePRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"})
	if !got.Found() || !got.Merged || got.URL != "https://github.com/owner/repo/pull/5" {
		t.Fatalf("observation = %+v, want the merged PR", got)
	}
}

// Proves a branch with only a closed-unmerged PR still resolves to it rather than treating the
// tier rule as requiring a merged candidate to exist.
func TestObservePRByBranchReturnsSoleClosedUnmergedPR(t *testing.T) {
	writeFakeGHPRList(t, `[{"number":9,"url":"https://github.com/owner/repo/pull/9","state":"CLOSED"}]`, 0, "")
	got := ObservePRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"})
	if !got.Found() || got.Merged || got.URL != "https://github.com/owner/repo/pull/9" {
		t.Fatalf("observation = %+v, want the sole closed-unmerged PR unmerged", got)
	}
}

func requireAmbiguous(t *testing.T, o PRObservation, wantCandidates int, named ...string) {
	t.Helper()
	// Ambiguity is unknown, never absent: a caller that read it as "no PR" would act on a branch
	// that demonstrably carries one.
	requireUnknown(t, o)
	if o.Ambiguous == nil {
		t.Fatalf("observation = %+v, want the candidates carried on the observation", o)
	}
	var ambiguous *AmbiguousPRError
	if !errors.As(error(o.Ambiguous), &ambiguous) {
		t.Fatalf("Ambiguous = %+v, want an AmbiguousPRError", o.Ambiguous)
	}
	if len(ambiguous.Candidates) != wantCandidates {
		t.Fatalf("Candidates = %+v, want %d of them named", ambiguous.Candidates, wantCandidates)
	}
	for _, want := range named {
		if !strings.Contains(ambiguous.Error(), want) {
			t.Fatalf("got %q, want %q named", ambiguous.Error(), want)
		}
	}
	if !strings.Contains(o.Reason(), ambiguous.Error()) {
		t.Fatalf("Reason() = %q, want it to carry %q", o.Reason(), ambiguous.Error())
	}
}

// Proves an ambiguous winning tier refuses rather than picking either candidate.
func TestObservePRByBranchRefusesTwoMergedPRs(t *testing.T) {
	writeFakeGHPRList(t, `[{"number":9,"url":"https://github.com/owner/repo/pull/9","state":"MERGED"},`+
		`{"number":5,"url":"https://github.com/owner/repo/pull/5","state":"MERGED"}]`, 0, "")
	requireAmbiguous(t, ObservePRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"}), 2, "#9", "#5")
}

// Proves the same-tier refusal names every PR on the head ref, including one sitting in a losing
// tier, so the operator resolves the whole branch rather than the pair that triggered the refusal.
func TestObservePRByBranchRefusesTwoMergedPRsNamesClosedCandidateToo(t *testing.T) {
	writeFakeGHPRList(t, `[{"number":7,"url":"https://github.com/owner/repo/pull/7","state":"MERGED"},`+
		`{"number":5,"url":"https://github.com/owner/repo/pull/5","state":"MERGED"},`+
		`{"number":3,"url":"https://github.com/owner/repo/pull/3","state":"CLOSED"}]`, 0, "")
	requireAmbiguous(t, ObservePRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"}), 3, "#7", "#5", "#3")
}

// Proves a branch carrying both a merged PR and a still-open one refuses rather than resolving to
// the merged PR: the open PR is live evidence the branch may carry unlanded work.
func TestObservePRByBranchRefusesMergedAndOpenPR(t *testing.T) {
	writeFakeGHPRList(t, `[{"number":5,"url":"https://github.com/owner/repo/pull/5","state":"MERGED"},`+
		`{"number":9,"url":"https://github.com/owner/repo/pull/9","state":"OPEN"}]`, 0, "")
	requireAmbiguous(t, ObservePRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"}), 2, "#5", "#9")
}

// Proves the merged+open refusal names every candidate on the branch, including a coexisting
// closed-unmerged PR, not just the merged and open ones.
func TestObservePRByBranchRefusesMergedAndOpenPRNamesClosedCandidateToo(t *testing.T) {
	writeFakeGHPRList(t, `[{"number":5,"url":"https://github.com/owner/repo/pull/5","state":"MERGED"},`+
		`{"number":9,"url":"https://github.com/owner/repo/pull/9","state":"OPEN"},`+
		`{"number":3,"url":"https://github.com/owner/repo/pull/3","state":"CLOSED"}]`, 0, "")
	requireAmbiguous(t, ObservePRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"}), 3, "#5", "#9", "#3")
}

// Pins the open-over-closed tier boundary: with no merged PR on the branch, an open PR beats a
// closed-unmerged one rather than either being an arbitrary loop-order pick.
func TestObservePRByBranchPrefersOpenOverClosedUnmerged(t *testing.T) {
	writeFakeGHPRList(t, `[{"number":9,"url":"https://github.com/owner/repo/pull/9","state":"CLOSED"},`+
		`{"number":5,"url":"https://github.com/owner/repo/pull/5","state":"OPEN"}]`, 0, "")
	got := ObservePRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"})
	if !got.Found() || got.Merged || got.URL != "https://github.com/owner/repo/pull/5" {
		t.Fatalf("observation = %+v, want the open PR", got)
	}
}

// A state vocabulary this tier pass does not know is unknown: the query completed and named
// candidates, which is neither this branch's PR nor a proven absence.
func TestObservePRByBranchIsUnknownForAnUnrecognizedState(t *testing.T) {
	writeFakeGHPRList(t, `[{"number":9,"url":"https://github.com/owner/repo/pull/9","state":"DRAFTED_SOMEHOW"}]`, 0, "")
	got := ObservePRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"})
	requireUnknown(t, got)
	if !strings.Contains(got.Reason(), "recognized state") {
		t.Fatalf("Reason() = %q, want it to name the state vocabulary as the problem", got.Reason())
	}
}

// Fakes `gh pr list` for a fork search, dispatching on the --repo argument the old single-body
// fake ignored: without that, no test can express "the PR is on repo B while the project's repo is
// A", and a fork test would pass against a shape gh never returns (atqamz/hand#40).
func writeFakeGHPRListPerRepo(t *testing.T, bodies map[string]string) {
	t.Helper()
	var responses []faketool.GHResponse
	for repo, body := range bodies {
		responses = append(responses, faketool.GHResponse{Command: "pr list", Repo: repo, Stdout: body})
	}
	writeFakeGHPRListResponses(t, responses...)
}

func writeFakeGHPRListResponses(t *testing.T, responses ...faketool.GHResponse) {
	t.Helper()
	faketool.GH{Responses: responses, RejectQualifiedHead: true}.Install(t, faketool.Bin(t))
}

// The target pair a fork project searches with: its own repo, where hand pushes the branch, plus
// the declared upstream the PR is opened on.
func forkTargets() []PRSearchTarget {
	return []PRSearchTarget{{Repo: "me/repo"}, {Repo: "up/repo", HeadRepo: "me/repo"}}
}

// The atqamz/hand#134 regression: a fork contribution's PR lives on the declared upstream,
// so searching the project's own repo alone finds nothing.
func TestObservePRByBranchFindsUpstreamPRForFork(t *testing.T) {
	writeFakeGHPRListPerRepo(t, map[string]string{
		"me/repo": `[]`,
		"up/repo": `[{"number":7,"url":"https://github.com/up/repo/pull/7","state":"MERGED",` +
			`"headRepository":{"nameWithOwner":"me/repo"}}]`,
	})
	got := ObservePRByBranch(context.Background(), "task-1-branch", forkTargets()...)
	if !got.Found() || !got.Merged || got.URL != "https://github.com/up/repo/pull/7" {
		t.Fatalf("observation = %+v, want the upstream PR merged", got)
	}
}

// A sweep is only as conclusive as its narrowest target: one repo answering "no PR here" while
// another never answered at all proves nothing about the branch.
func TestObservePRByBranchIsUnknownWhenOneTargetCouldNotBeSearched(t *testing.T) {
	writeFakeGHPRListResponses(t,
		faketool.GHResponse{Command: "pr list", Repo: "me/repo", Stdout: `[]`},
		faketool.GHResponse{Command: "pr list", Repo: "up/repo", Stderr: "gh: HTTP 401: Bad credentials\n", Exit: 1},
	)
	requireUnknown(t, ObservePRByBranch(context.Background(), "task-1-branch", forkTargets()...))
}

// Pins the head-repo filter: an upstream carries head refs from every contributor's fork, and gh
// matches --head on the branch name alone, so a same-named branch from a stranger's fork comes back
// from the same search and must not be recorded as this task's PR.
func TestObservePRByBranchIgnoresUpstreamPRFromAnotherFork(t *testing.T) {
	writeFakeGHPRListPerRepo(t, map[string]string{
		"me/repo": `[]`,
		"up/repo": `[{"number":7,"url":"https://github.com/up/repo/pull/7","state":"OPEN",` +
			`"headRepository":{"nameWithOwner":"someone/repo"}}]`,
	})
	got := ObservePRByBranch(context.Background(), "task-1-branch", forkTargets()...)
	if !got.Absent() {
		t.Fatalf("observation = %+v, want absent for a PR whose head ref lives in another fork", got)
	}
}

// Pins the fold: GitHub slugs are case-insensitive, and this filter compares gh's canonical casing
// against a slug read from a clone's origin remote, so a differently-cased remote must not drop the
// project's own PR.
func TestObservePRByBranchMatchesHeadRepoCaseInsensitively(t *testing.T) {
	writeFakeGHPRListPerRepo(t, map[string]string{
		"Up/Repo": `[{"number":7,"url":"https://github.com/up/repo/pull/7","state":"OPEN",` +
			`"headRepository":{"nameWithOwner":"Me/Repo"}}]`,
	})
	got := ObservePRByBranch(context.Background(), "task-1-branch", forkTargets()...)
	if !got.Found() || got.URL != "https://github.com/up/repo/pull/7" {
		t.Fatalf("observation = %+v, want the PR whose head repo differs only in casing", got)
	}
}

// Proves matches from two searched repos resolve through the same tier rule as two in one repo -
// a fork whose upstream also has a branch of that name is where guessing costs the most - and that
// the refusal names the repos, not just PR numbers an operator would have to hunt for.
func TestObservePRByBranchRefusesPRsInTwoRepos(t *testing.T) {
	writeFakeGHPRListPerRepo(t, map[string]string{
		"me/repo": `[{"number":3,"url":"https://github.com/me/repo/pull/3","state":"OPEN",` +
			`"headRepository":{"nameWithOwner":"me/repo"}}]`,
		"up/repo": `[{"number":7,"url":"https://github.com/up/repo/pull/7","state":"OPEN",` +
			`"headRepository":{"nameWithOwner":"me/repo"}}]`,
	})
	requireAmbiguous(t, ObservePRByBranch(context.Background(), "task-1-branch", forkTargets()...), 2, "me/repo#3", "up/repo#7")
}

// The one absence this package proves: every target answered, and none of them carries the branch.
func TestObservePRByBranchIsAbsentWhenEveryTargetAnsweredNothing(t *testing.T) {
	writeFakeGHPRList(t, `[]`, 0, "")
	got := ObservePRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"})
	if !got.Absent() {
		t.Fatalf("observation = %+v, want absent for an empty result", got)
	}
	if got.Unknown() {
		t.Fatalf("observation = %+v, want a completed empty search to be a finding, not unknown", got)
	}
}

// A prerequisite that never got as far as gh is unknown too, and says so with the command that
// failed rather than pretending GitHub answered.
func TestUnknownPRObservationCarriesItsOwnProbe(t *testing.T) {
	got := UnknownPRObservation("git rev-parse --abbrev-ref HEAD", "resolve the branch to search for: broken")
	requireUnknown(t, got)
	if !strings.Contains(got.Reason(), "git rev-parse") || !strings.Contains(got.Reason(), "broken") {
		t.Fatalf("Reason() = %q, want the failed prerequisite named", got.Reason())
	}
}

func TestRepoSlugFromRemote(t *testing.T) {
	cases := []struct {
		remote string
		slug   string
		ok     bool
	}{
		{"https://github.com/atqamz/hand", "atqamz/hand", true},
		{"https://github.com/atqamz/hand.git", "atqamz/hand", true},
		{"git@github.com:atqamz/hand.git", "atqamz/hand", true},
		{"ssh://git@github.com/atqamz/hand.git", "atqamz/hand", true},
		{"local", "", false},
		{"https://gitlab.com/atqamz/hand", "", false},
		{"https://github.com/atqamz", "", false},
		{"https://github.com/atqamz/hand/extra", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		slug, ok := RepoSlugFromRemote(c.remote)
		if slug != c.slug || ok != c.ok {
			t.Errorf("RepoSlugFromRemote(%q) = (%q, %v), want (%q, %v)", c.remote, slug, ok, c.slug, c.ok)
		}
	}
}

func TestObserveHeadCommitReadsTheRecordedHeadRef(t *testing.T) {
	faketool.GH{Responses: []faketool.GHResponse{{
		Command: "pr view",
		Stderr:  "Warning: gh version 2.40.0 is out of date",
		Stdout:  "{\"headRefOid\":\"1111111111111111111111111111111111111111\"}\n",
	}}}.Install(t, faketool.Bin(t))
	got := ObserveHeadCommit(context.Background(), "42")
	if !got.Found() || got.Head != "1111111111111111111111111111111111111111" {
		t.Fatalf("observation = %+v, want the recorded head commit", got)
	}
}

func TestObserveHeadCommitIsUnknownForAnEmptyHeadRef(t *testing.T) {
	faketool.GH{Responses: []faketool.GHResponse{{Command: "pr view", Stdout: "{}\n"}}}.Install(t, faketool.Bin(t))
	requireUnknown(t, ObserveHeadCommit(context.Background(), "42"))
}
