package cli

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/gitcfg"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

func (w *watcher) reapWorktrees(ctx context.Context) {
	day := w.r.env.Now().UTC().Format(snapshotLayout)
	if done, _ := w.reaped.Load().(string); day == done || !w.reapBusy.CompareAndSwap(false, true) {
		return
	}
	w.pending.Add(1)
	go func() {
		defer w.pending.Done()
		defer w.reapBusy.Store(false)
		if w.reap(ctx) && ctx.Err() == nil {
			w.reaped.Store(day)
		}
	}()
}

func (w *watcher) reap(ctx context.Context) bool {
	p, err := harness.LoadPolicy(w.r.home)
	if err != nil {
		if !w.reapWarned.Swap(true) {
			w.say(harness.PolicyFile + " unreadable, worktree retention stays off: " + err.Error())
		}
		return false
	}
	window := p.Retention.Worktrees
	if window == 0 {
		return true
	}
	attempts, err := w.st.UncleanedAttempts(ctx)
	if err != nil {
		return false
	}
	now := w.r.env.Now()
	var due []state.Attempt
	for _, a := range attempts {
		if ended, err := time.Parse(time.RFC3339Nano, a.EndedAt); err == nil && !a.Live() && now.Sub(ended) >= window {
			due = append(due, a)
		}
	}
	if len(due) == 0 {
		return true
	}
	c, _, err := w.r.luvus(ctx)
	if err != nil {
		return false
	}
	terms, err := c.Inventory(ctx)
	if err != nil {
		return false
	}
	for _, a := range due {
		if ctx.Err() != nil {
			return false
		}
		w.reapOne(ctx, c, terms, a, window)
	}
	return true
}

func (w *watcher) reapOne(ctx context.Context, c luvus.Client, terms []luvus.Terminal, a state.Attempt, window time.Duration) {
	unlock, ok, err := w.r.attemptLock(a.ID, false)
	if err != nil || !ok {
		return
	}
	defer unlock()
	if a, err = w.st.Attempt(ctx, a.ID); err != nil || a.Live() || a.CleanedAt != "" {
		return
	}
	reason, err := w.reapCheck(ctx, c, terms, a)
	if err == nil && reason == "" {
		err = w.reapRemove(ctx, c, a, window)
	}
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, state.ErrConflict) {
			return
		}
		reason = "could not check it: " + clipRunes(oneLine(err.Error()), 160)
	}
	if reason != "" {
		if err := w.st.KeepAttempt(ctx, a.ID, reason); err != nil && ctx.Err() == nil {
			w.say("keep " + state.AttemptRef(a.ID) + ": " + err.Error())
		}
	}
}

func (w *watcher) reapCheck(ctx context.Context, c luvus.Client, terms []luvus.Terminal, a state.Attempt) (string, error) {
	for _, t := range terms {
		if under(realRoots(a.Worktree), t.CWD) {
			return "a terminal is open in its worktree", nil
		}
	}
	g, err := w.repoOf(ctx, a)
	if err != nil {
		return "", err
	}
	return worktreeBlocker(ctx, g, a.Worktree)
}

func (w *watcher) repoOf(ctx context.Context, a state.Attempt) (gitcfg.Repo, error) {
	task, err := w.st.Task(ctx, a.TaskID)
	if err != nil {
		return gitcfg.Repo{}, err
	}
	project, err := w.st.Project(ctx, task.Project)
	if err != nil {
		return gitcfg.Repo{}, err
	}
	return repoGit(ctx, w.st, project)
}

func (w *watcher) reapRemove(ctx context.Context, c luvus.Client, a state.Attempt, window time.Duration) error {
	g, err := w.repoOf(ctx, a)
	if err != nil {
		return err
	}
	if err := removeWorktree(ctx, c, g, a.Worktree, false); err != nil {
		return err
	}
	_, warns := removeScratch(scanScratch(w.r.leftovers(a.Harness, a.Worktree), 0))
	for _, warn := range warns {
		w.say(state.AttemptRef(a.ID) + ": " + warn)
	}
	if _, err := w.st.CleanAttemptAfter(ctx, a.ID, strconv.Itoa(int(window/(24*time.Hour)))+"d"); err != nil {
		return err
	}
	w.say("removed the worktree of " + state.AttemptRef(a.ID) + " after " + strconv.Itoa(int(window/(24*time.Hour))) + "d")
	return nil
}

func worktreeBlocker(ctx context.Context, g gitcfg.Repo, dir string) (string, error) {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	here := g.In(dir)
	dirty, err := here.Git(ctx, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(dirty) != "" {
		return "it has uncommitted changes", nil
	}
	n, err := here.Git(ctx, "rev-list", "--count", "HEAD", "--not", "--remotes")
	if err != nil {
		return "", err
	}
	if n = strings.TrimSpace(n); n != "0" {
		return n + " commit(s) are on no remote branch: not pushed or merged", nil
	}
	return "", nil
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
