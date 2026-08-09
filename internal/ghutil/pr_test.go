package ghutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

// Fakes `gh pr view --json state`, emitting a stderr line ahead of the JSON payload so a
// CombinedOutput regression at the call site fails the parse the same way real gh's progress
// output does.
func writeFakeGHPRView(t *testing.T, state string, exitCode int, stderrLine string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\n"
	if stderrLine != "" {
		script += fmt.Sprintf("echo %q >&2\n", stderrLine)
	}
	if exitCode != 0 {
		script += fmt.Sprintf("exit %d\n", exitCode)
	} else {
		script += fmt.Sprintf("printf '{\"state\":\"%s\"}\\n'\n", state)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

func TestPRIsMergedIgnoresStderrNoise(t *testing.T) {
	for _, c := range []struct {
		state string
		want  bool
	}{{"MERGED", true}, {"OPEN", false}} {
		writeFakeGHPRView(t, c.state, 0, "Warning: gh version 2.40.0 is out of date")
		got, err := PRIsMerged(context.Background(), "42")
		if err != nil {
			t.Fatalf("PRIsMerged with state %s: %v", c.state, err)
		}
		if got != c.want {
			t.Errorf("PRIsMerged with state %s = %v, want %v", c.state, got, c.want)
		}
	}
}

func TestPRIsMergedReportsExitStatusWithoutStderr(t *testing.T) {
	writeFakeGHPRView(t, "", 1, "")
	_, err := PRIsMerged(context.Background(), "42")
	if err == nil {
		t.Fatal("want error when gh exits non-zero")
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
	bin := t.TempDir()
	script := "#!/bin/sh\n"
	if stderrLine != "" {
		script += fmt.Sprintf("echo %q >&2\n", stderrLine)
	}
	if exitCode != 0 {
		script += fmt.Sprintf("exit %d\n", exitCode)
	} else {
		script += fmt.Sprintf("printf '%s'\n", body)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

func TestFindPRByBranchReturnsMatch(t *testing.T) {
	writeFakeGHPRList(t, `[{"url":"https://github.com/owner/repo/pull/5","state":"MERGED"}]`, 0, "Warning: gh version 2.40.0 is out of date")
	url, merged, found, err := FindPRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if !found || url != "https://github.com/owner/repo/pull/5" || !merged {
		t.Fatalf("got (%q, %v, %v), want the merged PR", url, merged, found)
	}
}

// The atqamz/hand#77 regression: a branch carrying a merged PR alongside a closed-unmerged
// one (a duplicate opened by mistake, say) must resolve to the merged PR rather than an arbitrary
// pick.
func TestFindPRByBranchPrefersMergedOverClosedUnmerged(t *testing.T) {
	writeFakeGHPRList(t, `[{"number":9,"url":"https://github.com/owner/repo/pull/9","state":"CLOSED"},`+
		`{"number":5,"url":"https://github.com/owner/repo/pull/5","state":"MERGED"}]`, 0, "")
	url, merged, found, err := FindPRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if !found || !merged || url != "https://github.com/owner/repo/pull/5" {
		t.Fatalf("got (%q, %v, %v), want the merged PR", url, merged, found)
	}
}

// Proves a branch with only a closed-unmerged PR still resolves to it rather than treating the
// tier rule as requiring a merged candidate to exist.
func TestFindPRByBranchReturnsSoleClosedUnmergedPR(t *testing.T) {
	writeFakeGHPRList(t, `[{"number":9,"url":"https://github.com/owner/repo/pull/9","state":"CLOSED"}]`, 0, "")
	url, merged, found, err := FindPRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if !found || merged || url != "https://github.com/owner/repo/pull/9" {
		t.Fatalf("got (%q, %v, %v), want the sole closed-unmerged PR unmerged", url, merged, found)
	}
}

// Proves an ambiguous winning tier refuses rather than picking either candidate.
func TestFindPRByBranchRefusesTwoMergedPRs(t *testing.T) {
	writeFakeGHPRList(t, `[{"number":9,"url":"https://github.com/owner/repo/pull/9","state":"MERGED"},`+
		`{"number":5,"url":"https://github.com/owner/repo/pull/5","state":"MERGED"}]`, 0, "")
	_, _, _, err := FindPRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"})
	var ambiguous *AmbiguousPRError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("got %v, want an AmbiguousPRError", err)
	}
	if len(ambiguous.Candidates) != 2 {
		t.Fatalf("Candidates = %+v, want both PR 9 and PR 5 named", ambiguous.Candidates)
	}
	if !strings.Contains(err.Error(), "#9") || !strings.Contains(err.Error(), "#5") {
		t.Fatalf("got %q, want both PR numbers named", err.Error())
	}
}

// Proves the same-tier refusal names every PR on the head ref, including one sitting in a losing
// tier, so the operator resolves the whole branch rather than the pair that triggered the refusal.
func TestFindPRByBranchRefusesTwoMergedPRsNamesClosedCandidateToo(t *testing.T) {
	writeFakeGHPRList(t, `[{"number":7,"url":"https://github.com/owner/repo/pull/7","state":"MERGED"},`+
		`{"number":5,"url":"https://github.com/owner/repo/pull/5","state":"MERGED"},`+
		`{"number":3,"url":"https://github.com/owner/repo/pull/3","state":"CLOSED"}]`, 0, "")
	_, _, _, err := FindPRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"})
	var ambiguous *AmbiguousPRError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("got %v, want an AmbiguousPRError", err)
	}
	if len(ambiguous.Candidates) != 3 {
		t.Fatalf("Candidates = %+v, want PR 7, PR 5, and PR 3 all named", ambiguous.Candidates)
	}
	if !strings.Contains(err.Error(), "#7") || !strings.Contains(err.Error(), "#5") || !strings.Contains(err.Error(), "#3") {
		t.Fatalf("got %q, want all three PR numbers named", err.Error())
	}
}

// Proves a branch carrying both a merged PR and a still-open one refuses rather than resolving to
// the merged PR: the open PR is live evidence the branch may carry unlanded work.
func TestFindPRByBranchRefusesMergedAndOpenPR(t *testing.T) {
	writeFakeGHPRList(t, `[{"number":5,"url":"https://github.com/owner/repo/pull/5","state":"MERGED"},`+
		`{"number":9,"url":"https://github.com/owner/repo/pull/9","state":"OPEN"}]`, 0, "")
	_, _, _, err := FindPRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"})
	var ambiguous *AmbiguousPRError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("got %v, want an AmbiguousPRError", err)
	}
	if len(ambiguous.Candidates) != 2 {
		t.Fatalf("Candidates = %+v, want both PR 5 and PR 9 named", ambiguous.Candidates)
	}
	if !strings.Contains(err.Error(), "#5") || !strings.Contains(err.Error(), "#9") {
		t.Fatalf("got %q, want both PR numbers named", err.Error())
	}
}

// Proves the merged+open refusal names every candidate on the branch, including a coexisting
// closed-unmerged PR, not just the merged and open ones.
func TestFindPRByBranchRefusesMergedAndOpenPRNamesClosedCandidateToo(t *testing.T) {
	writeFakeGHPRList(t, `[{"number":5,"url":"https://github.com/owner/repo/pull/5","state":"MERGED"},`+
		`{"number":9,"url":"https://github.com/owner/repo/pull/9","state":"OPEN"},`+
		`{"number":3,"url":"https://github.com/owner/repo/pull/3","state":"CLOSED"}]`, 0, "")
	_, _, _, err := FindPRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"})
	var ambiguous *AmbiguousPRError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("got %v, want an AmbiguousPRError", err)
	}
	if len(ambiguous.Candidates) != 3 {
		t.Fatalf("Candidates = %+v, want PR 5, PR 9, and PR 3 all named", ambiguous.Candidates)
	}
	if !strings.Contains(err.Error(), "#5") || !strings.Contains(err.Error(), "#9") || !strings.Contains(err.Error(), "#3") {
		t.Fatalf("got %q, want all three PR numbers named", err.Error())
	}
}

// Pins the open-over-closed tier boundary: with no merged PR on the branch, an open PR beats a
// closed-unmerged one rather than either being an arbitrary loop-order pick.
func TestFindPRByBranchPrefersOpenOverClosedUnmerged(t *testing.T) {
	writeFakeGHPRList(t, `[{"number":9,"url":"https://github.com/owner/repo/pull/9","state":"CLOSED"},`+
		`{"number":5,"url":"https://github.com/owner/repo/pull/5","state":"OPEN"}]`, 0, "")
	url, merged, found, err := FindPRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if !found || merged || url != "https://github.com/owner/repo/pull/5" {
		t.Fatalf("got (%q, %v, %v), want the open PR", url, merged, found)
	}
}

// Fakes `gh pr list` for a fork search, dispatching on the --repo argument the old single-body
// fake ignored: without that, no test can express "the PR is on repo B while the project's repo is
// A", and a fork test would pass against a shape gh never returns (atqamz/hand#40).
func writeFakeGHPRListPerRepo(t *testing.T, bodies map[string]string) {
	t.Helper()
	bin := t.TempDir()

	// A qualified owner:branch --head value is refused below because real gh silently answers it
	// with an empty list - verified against gh 2.97 on a live cross-repo PR - so the mistake
	// surfaces as a failure here instead of as "no PR found" in production.
	script := "#!/bin/sh\n" +
		"[ \"$1 $2\" = \"pr list\" ] || { echo \"unexpected gh args: $@\" >&2; exit 1; }\n" +
		"[ \"$3\" = \"--repo\" ] && [ \"$5\" = \"--head\" ] || { echo \"unexpected gh args: $@\" >&2; exit 1; }\n" +
		"case \"$6\" in *:*) echo \"qualified head ref matches nothing in real gh: $6\" >&2; exit 1 ;; esac\n" +
		"case \"$4\" in\n"
	// The dispatch case-folds --repo because GitHub serves a repo under any casing of its slug, so a
	// case-sensitive fake would answer a differently-cased target with an empty list and hide the
	// hit real gh returns.
	for repo, body := range bodies {
		script += fmt.Sprintf("%s) printf '%s' ;;\n", anyCasingGlob(repo), body)
	}
	script += "*) printf '[]' ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

// Turns a repo slug into an unquoted sh case pattern matching it in any casing - a glob rather than
// tr, because PATH is replaced by the fake's own dir alone and no external binary resolves inside
// these scripts. Safe unquoted: a slug is [A-Za-z0-9._-/] only, so it carries no metacharacter.
func anyCasingGlob(slug string) string {
	var b strings.Builder
	for _, r := range slug {
		lower, upper := unicode.ToLower(r), unicode.ToUpper(r)
		if lower == upper {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('[')
		b.WriteRune(upper)
		b.WriteRune(lower)
		b.WriteByte(']')
	}
	return b.String()
}

// The target pair a fork project searches with: its own repo, where hand pushes the branch, plus
// the declared upstream the PR is opened on.
func forkTargets() []PRSearchTarget {
	return []PRSearchTarget{{Repo: "me/repo"}, {Repo: "up/repo", HeadRepo: "me/repo"}}
}

// The atqamz/hand#134 regression: a fork contribution's PR lives on the declared upstream,
// so searching the project's own repo alone finds nothing.
func TestFindPRByBranchFindsUpstreamPRForFork(t *testing.T) {
	writeFakeGHPRListPerRepo(t, map[string]string{
		"me/repo": `[]`,
		"up/repo": `[{"number":7,"url":"https://github.com/up/repo/pull/7","state":"MERGED",` +
			`"headRepository":{"nameWithOwner":"me/repo"}}]`,
	})
	url, merged, found, err := FindPRByBranch(context.Background(), "task-1-branch", forkTargets()...)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !merged || url != "https://github.com/up/repo/pull/7" {
		t.Fatalf("got (%q, %v, %v), want the upstream PR merged", url, merged, found)
	}
}

// Pins the head-repo filter: an upstream carries head refs from every contributor's fork, and gh
// matches --head on the branch name alone, so a same-named branch from a stranger's fork comes back
// from the same search and must not be recorded as this task's PR.
func TestFindPRByBranchIgnoresUpstreamPRFromAnotherFork(t *testing.T) {
	writeFakeGHPRListPerRepo(t, map[string]string{
		"me/repo": `[]`,
		"up/repo": `[{"number":7,"url":"https://github.com/up/repo/pull/7","state":"OPEN",` +
			`"headRepository":{"nameWithOwner":"someone/repo"}}]`,
	})
	_, _, found, err := FindPRByBranch(context.Background(), "task-1-branch", forkTargets()...)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("want found=false for a PR whose head ref lives in another fork")
	}
}

// Pins the fold: GitHub slugs are case-insensitive, and this filter compares gh's canonical casing
// against a slug read from a clone's origin remote, so a differently-cased remote must not drop the
// project's own PR.
func TestFindPRByBranchMatchesHeadRepoCaseInsensitively(t *testing.T) {
	writeFakeGHPRListPerRepo(t, map[string]string{
		"Up/Repo": `[{"number":7,"url":"https://github.com/up/repo/pull/7","state":"OPEN",` +
			`"headRepository":{"nameWithOwner":"Me/Repo"}}]`,
	})
	url, _, found, err := FindPRByBranch(context.Background(), "task-1-branch", forkTargets()...)
	if err != nil {
		t.Fatal(err)
	}
	if !found || url != "https://github.com/up/repo/pull/7" {
		t.Fatalf("got (%q, %v), want the PR whose head repo differs only in casing", url, found)
	}
}

// Proves matches from two searched repos resolve through the same tier rule as two in one repo -
// a fork whose upstream also has a branch of that name is where guessing costs the most - and that
// the refusal names the repos, not just PR numbers an operator would have to hunt for.
func TestFindPRByBranchRefusesPRsInTwoRepos(t *testing.T) {
	writeFakeGHPRListPerRepo(t, map[string]string{
		"me/repo": `[{"number":3,"url":"https://github.com/me/repo/pull/3","state":"OPEN",` +
			`"headRepository":{"nameWithOwner":"me/repo"}}]`,
		"up/repo": `[{"number":7,"url":"https://github.com/up/repo/pull/7","state":"OPEN",` +
			`"headRepository":{"nameWithOwner":"me/repo"}}]`,
	})
	_, _, _, err := FindPRByBranch(context.Background(), "task-1-branch", forkTargets()...)
	var ambiguous *AmbiguousPRError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("got %v, want an AmbiguousPRError", err)
	}
	if len(ambiguous.Candidates) != 2 {
		t.Fatalf("Candidates = %+v, want both PRs named", ambiguous.Candidates)
	}
	if !strings.Contains(err.Error(), "me/repo#3") || !strings.Contains(err.Error(), "up/repo#7") {
		t.Fatalf("got %q, want each candidate named with its repo", err.Error())
	}
}

func TestFindPRByBranchNoMatch(t *testing.T) {
	writeFakeGHPRList(t, `[]`, 0, "")
	_, _, found, err := FindPRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("want found=false for an empty result")
	}
}

func TestFindPRByBranchReportsExitStatusWithoutStderr(t *testing.T) {
	writeFakeGHPRList(t, "", 1, "")
	_, _, _, err := FindPRByBranch(context.Background(), "task-1-branch", PRSearchTarget{Repo: "owner/repo"})
	if err == nil {
		t.Fatal("want error when gh exits non-zero")
	}
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("got %q, want the exit status in the message", err)
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
