// Package ghutil shells out to the gh CLI for PR access rather than calling the
// REST API directly; internal/selfupdate follows the same convention for
// GitHub Releases. Tests fake gh with a shell script on PATH (see writeFakeGHPRView
// in pr_test.go; the same pattern covers releases in
// internal/selfupdate/selfupdate_test.go and herdr in cmd/status_test.go).
package ghutil

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ObserveMergeState reports what GitHub records for one pull request's merge state. A query that
// did not complete is unknown, never an unmerged pull request, because "not merged" is what
// licenses teardown and reconciliation to act on work that may in fact have landed.
func ObserveMergeState(ctx context.Context, pr string) PRObservation {
	args := []string{"pr", "view", pr, "--json", "state"}
	probe := Probe{Command: ghCommand(args)}
	var body struct {
		State string `json:"state"`
	}
	if terminal := decodeGHPayload(ctx, probe, &body, args...); terminal != nil {
		return *terminal
	}
	if body.State == "" {
		return unknownPR(probe, "gh exited zero and reported no state for pull request %s", pr)
	}
	return PRObservation{State: ObservationFound, URL: pr, Merged: body.State == "MERGED", Probe: probe}
}

// ObserveHeadCommit reads the commit GitHub records as the PR head ref: the newest commit of that
// branch GitHub was observed holding, which outlives any local clone and survives the head branch
// being deleted after a merge.
func ObserveHeadCommit(ctx context.Context, pr string) PRObservation {
	args := []string{"pr", "view", pr, "--json", "headRefOid"}
	probe := Probe{Command: ghCommand(args)}
	var body struct {
		HeadRefOid string `json:"headRefOid"`
	}
	if terminal := decodeGHPayload(ctx, probe, &body, args...); terminal != nil {
		return *terminal
	}
	if body.HeadRefOid == "" {
		return unknownPR(probe, "gh exited zero and reported no head commit for pull request %s", pr)
	}
	return PRObservation{State: ObservationFound, URL: pr, Head: body.HeadRefOid, Probe: probe}
}

// PRIsMerged is the pre-tri-state entry point ObserveMergeState replaced, kept only while
// internal/watcher is its last caller (atqamz/hand#235 and atqamz/hand#252 own that surface).
// Nothing else may call it: a bare bool is what let a failed query read as an unmerged PR.
func PRIsMerged(ctx context.Context, pr string) (bool, error) {
	observation := ObserveMergeState(ctx, pr)
	if !observation.Found() {
		return false, errors.New(observation.Reason())
	}
	return observation.Merged, nil
}

// PRSearchTarget names one repo ObservePRByBranch searches for a head ref.
type PRSearchTarget struct {
	Repo string
	// When set, keeps only PRs whose head branch lives in that repo: a fork's upstream carries head
	// refs from every contributor, so a branch name alone can match a stranger's PR there. Folded,
	// because gh reports canonical casing while this comes from the clone's origin remote.
	HeadRepo string
}

// PRCandidate names one PR under consideration by ObservePRByBranch, for use in
// an AmbiguousPRError message.
type PRCandidate struct {
	Repo   string
	Number int
	State  string
}

// AmbiguousPRError reports that a branch's PRs do not resolve to a usable winner: no preference
// tier yields a single match, or a merged PR coexists with an open one, which refuses by rule.
// Candidates names every PR on the head ref, any state and any searched repo, not just that tier.
type AmbiguousPRError struct {
	Branch     string
	Candidates []PRCandidate
}

func (e *AmbiguousPRError) Error() string {
	parts := make([]string, len(e.Candidates))
	for i, c := range e.Candidates {
		parts[i] = fmt.Sprintf("%s#%d (%s)", c.Repo, c.Number, c.State)
	}
	return fmt.Sprintf("ambiguous PR for branch %s: %s", e.Branch, strings.Join(parts, ", "))
}

// ObservePRByBranch reports the PR across targets whose head ref is exactly branch - the only rule
// hand uses to associate a PR with a task, never a title, an issue number or a task id. Absent
// means every target answered, and none of them carries that head ref.
func ObservePRByBranch(ctx context.Context, branch string, targets ...PRSearchTarget) PRObservation {
	// More than one target is how a fork project finds its PR: the branch is pushed to the fork
	// while the PR is opened on the declared upstream. Matches from every target resolve through
	// one tier pass, so a fork PR and an upstream PR are ambiguous exactly like two in one repo.
	commands := make([]string, 0, len(targets))
	for _, target := range targets {
		commands = append(commands, ghCommand(prListArgs(target, branch)))
	}
	probe := Probe{Command: strings.Join(commands, "; ")}
	var results []prListItem
	for _, target := range targets {
		found, terminal := listPRsByBranch(ctx, target, branch)
		// A sweep that skipped a target cannot prove absence across the rest, so an observation
		// that did not complete ends it. An absence proven for one target is not one for the others.
		if terminal != nil && terminal.Unknown() {
			return *terminal
		}
		results = append(results, found...)
	}
	if len(results) == 0 {
		return absentPR(probe)
	}

	// A branch can carry more than one PR - a closed-unmerged one plus a reopened replacement - so
	// results resolve by preference tier rather than arbitrarily: merged, then open, then
	// closed-unmerged, and a tier holding more than one match is ambiguous.
	var mergedPRs, openPRs, closedPRs []prListItem
	for _, r := range results {
		switch r.State {
		case "MERGED":
			mergedPRs = append(mergedPRs, r)
		case "OPEN":
			openPRs = append(openPRs, r)
		case "CLOSED":
			closedPRs = append(closedPRs, r)
		}
	}

	// A merged PR coexisting with an open one refuses too: the open PR is live evidence the branch
	// may still carry unlanded work. Guessing here is what let internal/runtime/teardown.go's landed-work guard
	// trust a merged PR while the branch's real state was closed-unmerged (atqamz/hand#77).
	if len(mergedPRs) > 0 && len(openPRs) > 0 {
		return ambiguousPR(branch, results, probe)
	}

	for _, matches := range [][]prListItem{mergedPRs, openPRs, closedPRs} {
		switch len(matches) {
		case 0:
			continue
		case 1:
			return PRObservation{State: ObservationFound, URL: matches[0].URL, Merged: matches[0].State == "MERGED", Probe: probe}
		default:
			return ambiguousPR(branch, results, probe)
		}
	}
	// The query completed and carried candidates, so this is neither a found PR nor a proven
	// absence: gh answered in a state vocabulary this tier pass does not know.
	return unknownPR(probe, "gh reported %d PR(s) for branch %s and none in a recognized state", len(results), branch)
}

// --state all because gh pr list defaults to open only and a gate-opened PR may already be
// merged or closed; --limit stated rather than left on gh's implicit 30, far above any real
// count for one branch, so a same-tier duplicate cannot be truncated into a lone winner.
func prListArgs(target PRSearchTarget, branch string) []string {
	return []string{"pr", "list", "--repo", target.Repo, "--head", branch, "--state", "all", "--limit", "200", "--json", "number,url,state,headRepository"}
}

func listPRsByBranch(ctx context.Context, target PRSearchTarget, branch string) ([]prListItem, *PRObservation) {
	args := prListArgs(target, branch)
	probe := Probe{Command: ghCommand(args)}
	var results []prListItem
	if terminal := decodeGHPayload(ctx, probe, &results, args...); terminal != nil {
		return nil, terminal
	}
	// gh's --head takes the plain branch name even for a cross-repo PR (the qualified owner:branch
	// form matches nothing), so the upstream target carries a HeadRepo to keep a same-named branch
	// from another fork out.
	kept := make([]prListItem, 0, len(results))
	for _, r := range results {
		if target.HeadRepo != "" && !strings.EqualFold(r.HeadRepository.NameWithOwner, target.HeadRepo) {
			continue
		}
		r.Repo = target.Repo
		kept = append(kept, r)
	}
	return kept, nil
}

// Ambiguity is unknown rather than absent: the query completed, and what it returned says a PR
// exists without saying which one, so nothing about this branch may be concluded.
func ambiguousPR(branch string, matches []prListItem, probe Probe) PRObservation {
	candidates := make([]PRCandidate, len(matches))
	for i, m := range matches {
		candidates[i] = PRCandidate{Repo: m.Repo, Number: m.Number, State: m.State}
	}
	ambiguous := &AmbiguousPRError{Branch: branch, Candidates: candidates}
	observation := unknownPR(probe, "%s", ambiguous.Error())
	observation.Ambiguous = ambiguous
	return observation
}

func ghCommand(args []string) string {
	return "gh " + strings.Join(args, " ")
}

// One entry of `gh pr list --json number,url,state,headRepository`. Repo is the searched repo, not
// part of gh's payload: a match's own repo has to survive into an AmbiguousPRError naming PRs from
// two repos at once.
type prListItem struct {
	Number         int    `json:"number"`
	URL            string `json:"url"`
	State          string `json:"state"`
	HeadRepository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"headRepository"`
	Repo string `json:"-"`
}

// RepoSlugFromRemote extracts "owner/repo" from a GitHub origin remote URL in
// https, ssh, or git@ form. Returns ok=false on anything else, so a caller
// like hand pr can refuse rather than guess which repo a PR belongs to.
func RepoSlugFromRemote(remoteURL string) (string, bool) {
	s := strings.TrimSuffix(remoteURL, ".git")
	switch {
	case strings.HasPrefix(s, "https://github.com/"):
		s = strings.TrimPrefix(s, "https://github.com/")
	case strings.HasPrefix(s, "ssh://git@github.com/"):
		s = strings.TrimPrefix(s, "ssh://git@github.com/")
	case strings.HasPrefix(s, "git@github.com:"):
		s = strings.TrimPrefix(s, "git@github.com:")
	default:
		return "", false
	}
	parts := strings.Split(s, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
}
