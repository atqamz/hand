package cli

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/gitcfg"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/proc"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

const proposeBranch = "hand-skills/"

type proposal struct {
	repo, clone, branch, base, stat, commit string
}

func (p proposal) url() string { return "https://github.com/" + p.repo + ".git" }

func cmdSkillsPropose(r *runner, args []string) error {
	s, rest, err := r.skillScope("propose", args, 1, nil)
	if err != nil {
		return err
	}
	name := rest[0]
	if !fleet.SkillName.MatchString(name) {
		return fmt.Errorf("%w: %q is not a skill name", state.ErrInvalid, name)
	}
	defer r.closeFetches()
	p, err := r.prepareProposal(s, name)
	if err != nil {
		return err
	}
	url, err := r.pushProposal(name, p)
	if err != nil {
		return err
	}
	var d toon.Doc
	d.Rows("skills", []string{"name", "state", "detail"}, [][]string{{name, "proposed", url}})
	return r.print(&d)
}

func (r *runner) prepareProposal(s fleet.Skills, name string) (proposal, error) {
	unlock, err := s.Lock()
	if err != nil {
		return proposal{}, err
	}
	defer unlock()
	managed, err := s.Load()
	if err != nil {
		return proposal{}, err
	}
	e, ok := managed[name]
	if !ok {
		return proposal{}, fmt.Errorf("%w: %s is not a skill Hand manages in this scope", state.ErrInvalid, name)
	}
	m := githubRepo.FindStringSubmatch(e.URL())
	if e.Local() || m == nil {
		return proposal{}, fmt.Errorf("%w: %s comes from %s, which is not a GitHub repository (https://github.com/OWNER/REPO); move the edit into its source by hand", state.ErrInvalid, name, e.URL())
	}
	if _, err := harness.LookPath("gh", r.env.Getenv("PATH")); err != nil {
		return proposal{}, fmt.Errorf("propose opens its pull request with `gh pr create`: %w", err)
	}
	probe, err := r.compare(s, name, e)
	if err != nil {
		return proposal{}, err
	}
	defer os.RemoveAll(probe.staged)
	switch probe.state {
	case "edited":
	case "conflict":
		return proposal{}, fmt.Errorf("%w: %s is a conflict, changed here and moved at its source; nothing proposed; reconcile first: `%s` prints the task for it", state.ErrConflict, name, strings.TrimSpace(r.env.command()+" skills sync "+r.scopeArg))
	default:
		return proposal{}, fmt.Errorf("%w: %s is %s, not edited here; there is nothing to propose", state.ErrConflict, name, probe.state)
	}
	_, root, err := s.Check(name, e)
	if err != nil {
		return proposal{}, err
	}
	edited := s.Dir(root, name)
	sum, err := fleet.ComputedHash(edited)
	if err != nil {
		return proposal{}, err
	}
	c := r.clone(e.URL(), cmp.Or(e.Ref, "HEAD"))
	if c.err != nil {
		return proposal{}, c.err
	}
	p := proposal{repo: m[1], clone: c.repo, commit: c.commit, branch: proposeBranch + name + "-" + sum[:8]}
	if out, err := gitcfg.RunBounded(r.fetches.ctx, c.repo, skillGitTimeout, "ls-remote", "--heads", "--", p.url(), "refs/heads/"+p.branch); err != nil {
		return proposal{}, err
	} else if strings.TrimSpace(out) != "" {
		return proposal{}, fmt.Errorf("%w: this edit of %s was already proposed: the branch %s is on %s; open its pull request, or delete the branch to propose again", state.ErrConflict, name, p.branch, p.repo)
	}
	if e.Ref != "" {
		heads, err := gitcfg.RunBounded(r.fetches.ctx, c.repo, skillGitTimeout, "ls-remote", "--heads", "--", e.URL(), "refs/heads/"+e.Ref)
		if err != nil {
			return proposal{}, err
		}
		if strings.TrimSpace(heads) != "" {
			p.base = e.Ref
		}
	}
	if _, err := gitcfg.RunBounded(r.fetches.ctx, c.repo, skillGitTimeout, "checkout", "--quiet", "-b", p.branch); err != nil {
		return proposal{}, err
	}
	dir, err := clonedSkillDir(c.repo, e.Path())
	if err != nil {
		return proposal{}, err
	}
	if err := replaceContents(dir, edited); err != nil {
		return proposal{}, err
	}
	if err := fleet.Unmark(dir); err != nil {
		return proposal{}, err
	}
	if _, err := gitcfg.RunBounded(r.fetches.ctx, c.repo, skillGitTimeout, "add", "-A"); err != nil {
		return proposal{}, err
	}
	ignored, err := gitcfg.RunBounded(r.fetches.ctx, c.repo, skillGitTimeout, "ls-files", "--others", "--ignored", "--exclude-standard", "--", cmp.Or(e.Path(), "."))
	if err != nil {
		return proposal{}, err
	}
	if left := strings.Fields(ignored); len(left) > 0 {
		return proposal{}, fmt.Errorf("%w: %s holds files the source's .gitignore leaves out of a commit (%s); nothing proposed; move them out of the skill or change the ignore rule in the source", state.ErrInvalid, name, strings.Join(left[:min(len(left), 5)], ", "))
	}
	if p.stat, err = gitcfg.RunBounded(r.fetches.ctx, c.repo, skillGitTimeout, "diff", "--cached", "--stat", "--no-color"); err != nil {
		return proposal{}, err
	}
	if strings.TrimSpace(p.stat) == "" {
		return proposal{}, fmt.Errorf("%w: %s differs from its source only by Hand's marker; there is nothing to propose", state.ErrConflict, name)
	}
	return p, nil
}

func replaceContents(dst, src string) error {
	entries, err := os.ReadDir(dst)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() != ".git" {
			if err := os.RemoveAll(filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
	}
	return fleet.Copy(src, dst)
}

func (r *runner) pushProposal(name string, p proposal) (string, error) {
	ctx := r.fetches.ctx
	title := "skills: update " + name
	if _, err := gitcfg.RunBoundedHooks(ctx, p.clone, skillGitTimeout, "commit", "--quiet", "-m", title); err != nil {
		return "", fmt.Errorf("commit: %w; the commit uses your git identity (user.name, user.email) and your hooks and signing settings, and Hand changes none of them", err)
	}
	if _, err := gitcfg.RunBoundedHooks(ctx, p.clone, skillGitTimeout, "push", "--quiet", "--", p.url(), "HEAD:refs/heads/"+p.branch); err != nil {
		return "", err
	}
	body := fmt.Sprintf("Edited copy of %s, sent by `hand skills propose`. Based on %s.\n\n```\n%s```\n", name, p.commit, p.stat)
	url, err := r.ghCreate(ctx, p, title, body)
	if err != nil {
		return "", fmt.Errorf("pushed the branch %s to %s but `gh pr create` failed: %w; open the pull request from that branch by hand", p.branch, p.repo, err)
	}
	return url, nil
}

func (r *runner) ghCreate(ctx context.Context, p proposal, title, body string) (string, error) {
	gh, err := harness.LookPath("gh", r.env.Getenv("PATH"))
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, skillGitTimeout)
	defer cancel()
	args := []string{"pr", "create", "--repo", p.repo, "--head", p.branch}
	if p.base != "" {
		args = append(args, "--base", p.base)
	}
	cmd := exec.CommandContext(ctx, gh, append(args, "--title", title, "--body", body)...)
	cmd.Dir = p.clone
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "NO_COLOR=1")
	proc.NoWindow(cmd)
	proc.KillGroupOnCancel(cmd)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}
