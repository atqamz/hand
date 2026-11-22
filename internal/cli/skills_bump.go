package cli

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/gitcfg"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
)

const (
	bumpBranch    = "hand-skills/bump-"
	bumpBodyLimit = 60000
)

var projectRemote = regexp.MustCompile(`^(?:https://github\.com/|git@github\.com:|ssh://git@github\.com/)([^/]+/[^/]+?)(?:\.git)?/?$`)

func bumpVersion(commit, sum string) string {
	return unsafeWord.ReplaceAllString(short(cmp.Or(commit, sum)), "?")
}

func bumpTitle(name string, e fleet.Managed, commit, sum string) string {
	return fmt.Sprintf("skills: %s %s -> %s", name, bumpVersion(e.Commit, e.ComputedHash), bumpVersion(commit, sum))
}

var scpUserinfo = regexp.MustCompile(`^[^/@]*@`)

func shownURL(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		u.User, u.RawQuery, u.Fragment, u.ForceQuery = nil, "", "", false
		return u.String()
	}
	return scpUserinfo.ReplaceAllString(raw, "")
}

func unsecret(text, raw string) string {
	shown := shownURL(raw)
	if shown == raw {
		return text
	}
	quoted, _ := json.Marshal(raw)
	shownQuoted, _ := json.Marshal(shown)
	text = strings.NewReplacer(raw, shown, string(quoted[1:len(quoted)-1]), string(shownQuoted[1:len(shownQuoted)-1])).Replace(text)
	if u, err := url.Parse(raw); err == nil && u.User != nil {
		if pw, _ := u.User.Password(); pw != "" {
			text = strings.ReplaceAll(text, pw, "")
		}
	}
	return text
}

func bumpBody(title string, e fleet.Managed, diff string) string {
	source := shownURL(e.URL())
	diff = unsecret(diff, e.URL())
	if p := e.Path(); p != "" {
		source += "#" + p
	}
	note := ""
	if len(diff) > bumpBodyLimit {
		diff = diff[:strings.LastIndex(diff[:bumpBodyLimit], "\n")+1]
		note = fmt.Sprintf("\nThe diff is cut at %d bytes; Files changed has all of it.\n", bumpBodyLimit)
	}
	fence := "```"
	for strings.Contains(diff, fence) {
		fence += "`"
	}
	return fmt.Sprintf("%s\n\nSource: %s, ref %s. Opened by `hand skills sync`; the checkout it ran in is unchanged. Merge it through this repository's normal gate, then pull.\n\nThe change as the branch holds it:\n\n%sdiff\n%s%s%s\n", title, source, cmp.Or(e.Ref, "HEAD"), fence, diff, fence, note)
}

func (r *runner) bump(s fleet.Skills, name string, e fleet.Managed, p probed) (string, string, error) {
	remote, err := gitcfg.Run(r.ctx(), s.Base, "config", "--get", "remote.origin.url")
	remote = strings.TrimSpace(remote)
	m := projectRemote.FindStringSubmatch(remote)
	if err != nil || m == nil {
		return "", "", fmt.Errorf("%w: the remote origin of %s is not a GitHub repository (https://github.com/OWNER/REPO); a bump pull request needs one; run `%s skills update %s%s` to change the checkout directly", state.ErrInvalid, s.Base, r.env.command(), r.scopeArg, name)
	}
	if _, err := harness.LookPath("gh", r.env.Getenv("PATH")); err != nil {
		return "", "", fmt.Errorf("a bump opens its pull request with `gh pr create`: %w", err)
	}
	c := r.fetchClone(remote, "HEAD")
	if c.err != nil {
		return "", "", c.err
	}
	ctx := r.fetches.ctx
	head, err := gitcfg.RunBounded(ctx, c.repo, skillGitTimeout, "ls-remote", "--symref", "--", remote, "HEAD")
	if err != nil {
		return "", "", err
	}
	base, ok := strings.CutPrefix(strings.TrimSpace(strings.SplitN(head, "\n", 2)[0]), "ref: refs/heads/")
	base, _, _ = strings.Cut(base, "\t")
	if !ok || base == "" {
		return "", "", fmt.Errorf("%w: %s has no default branch to open a pull request against", state.ErrInvalid, m[1])
	}
	pr := proposal{repo: m[1], remote: remote, clone: c.repo, base: base, commit: c.commit, branch: bumpBranch + name + "-" + p.sum[:8]}
	if out, err := gitcfg.RunBounded(ctx, c.repo, skillGitTimeout, "ls-remote", "--heads", "--", remote, "refs/heads/"+pr.branch); err != nil {
		return "", "", err
	} else if strings.TrimSpace(out) != "" {
		return "pending", fmt.Sprintf("the branch %s is on %s from an earlier run; merge its pull request and pull, or delete the branch to send it again; if that run stopped at `gh pr create`, open the pull request from the branch by hand", pr.branch, pr.repo), nil
	}
	cs := fleet.Skills{Base: c.repo, Scope: "project", Meta: filepath.Join(c.repo, ".git", "hand-skills")}
	unlock, err := cs.Lock()
	if err != nil {
		return "", "", err
	}
	defer unlock()
	managed, err := cs.Load()
	if err != nil {
		return "", "", err
	}
	ce := managed[name]
	switch {
	case ce.ComputedHash == p.sum:
		return "pending", fmt.Sprintf("the default branch of %s already holds this update; pull the checkout", pr.repo), nil
	case ce.Commit != e.Commit:
		return "", "", fmt.Errorf("%w: the default branch of %s does not hold the copy of %s this checkout has (%s); merge or push the checkout's copy first, or pull", state.ErrConflict, pr.repo, name, commitWord(e.Commit))
	}
	if kind, _, err := cs.Check(name, ce); err != nil || kind != "" {
		return "", "", fmt.Errorf("%w: the copy of %s on the default branch of %s is not clean (%s); nothing sent", state.ErrConflict, name, pr.repo, cmp.Or(kind, fmt.Sprint(err)))
	}
	if _, err := gitcfg.RunBounded(ctx, c.repo, skillGitTimeout, "checkout", "--quiet", "-b", pr.branch); err != nil {
		return "", "", err
	}
	if err := cs.Install(name, p.staged, nil); err != nil {
		return "", "", err
	}
	ce.Commit, ce.ComputedHash, ce.Next = p.commit, p.sum, ""
	managed[name] = ce
	if err := cs.Save(managed); err != nil {
		return "", "", err
	}
	if _, err := gitcfg.RunBounded(ctx, c.repo, skillGitTimeout, "add", "-A"); err != nil {
		return "", "", err
	}
	if err := r.refuseIgnored(c.repo, name, ".", "project", "sent"); err != nil {
		return "", "", err
	}
	diff, err := gitcfg.RunBounded(ctx, c.repo, skillGitTimeout, "diff", "--cached", "--no-color", "--no-ext-diff", "--no-textconv")
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(diff) == "" {
		return "", "", fmt.Errorf("%w: %s changes nothing on the default branch of %s; nothing sent", state.ErrConflict, name, pr.repo)
	}
	title := bumpTitle(name, e, p.commit, p.sum)
	url, err := r.pushBranch(pr, title, bumpBody(title, e, diff))
	if err != nil {
		return "", "", err
	}
	return "bumped", url, nil
}
