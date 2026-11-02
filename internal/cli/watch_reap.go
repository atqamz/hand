package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/gitcfg"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/retention"
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
	for _, a := range due {
		if ctx.Err() != nil {
			return false
		}
		terms, err := c.Inventory(ctx)
		if err != nil {
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
	reason, ign, err := w.reapCheck(ctx, terms, a)
	if err == nil && reason == "" {
		err = w.reapRemove(ctx, c, a, window, ign)
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

func (w *watcher) reapCheck(ctx context.Context, terms []luvus.Terminal, a state.Attempt) (string, ignored, error) {
	for _, t := range terms {
		if under(realRoots(a.Worktree), t.CWD) {
			return "a terminal is open in its worktree", ignored{}, nil
		}
	}
	if worktreeBusy(a.Worktree) {
		return "a process is running in it", ignored{}, nil
	}
	g, err := w.repoOf(ctx, a)
	if err != nil {
		return "", ignored{}, err
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

func (w *watcher) reapRemove(ctx context.Context, c luvus.Client, a state.Attempt, window time.Duration, ign ignored) error {
	g, err := w.repoOf(ctx, a)
	if err != nil {
		return err
	}
	if err := removeWorktree(ctx, c, g, a.Worktree, false, false); err != nil {
		return err
	}
	_, warns := removeScratch(scanScratch(w.r.leftovers(a.Harness, a.Worktree), 0))
	for _, warn := range warns {
		w.say(state.AttemptRef(a.ID) + ": " + warn)
	}
	after := strconv.Itoa(int(window/(24*time.Hour))) + "d"
	if ign.files > 0 {
		after += "; " + strconv.Itoa(ign.files) + " ignored file(s), " + retention.Size(ign.bytes) + " removed"
	}
	if _, err := w.st.CleanAttemptAfter(ctx, a.ID, after); err != nil {
		return err
	}
	w.say("removed the worktree of " + state.AttemptRef(a.ID) + " after " + after)
	return nil
}

type ignored struct {
	files int
	bytes int64
}

var configLike = []string{".env*", "*.local", "*.pem", "*.key", "*credential*", "*secret*", ".npmrc"}

func looksLikeConfig(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	for _, pat := range configLike {
		if ok, _ := filepath.Match(pat, base); ok {
			return true
		}
	}
	return false
}

func worktreeBlocker(ctx context.Context, g gitcfg.Repo, dir string) (string, ignored, error) {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return "", ignored{}, nil
	} else if err != nil {
		return "", ignored{}, err
	}
	here := g.In(dir)
	dirty, err := here.Git(ctx, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "", ignored{}, err
	}
	if strings.TrimSpace(dirty) != "" {
		return "it has uncommitted changes", ignored{}, nil
	}
	n, err := here.Git(ctx, "rev-list", "--count", "HEAD", "--not", "--remotes")
	if err != nil {
		return "", ignored{}, err
	}
	if n = strings.TrimSpace(n); n != "0" {
		return n + " commit(s) are on no remote branch: not pushed or merged", ignored{}, nil
	}
	list, err := here.Git(ctx, "ls-files", "-z", "--others", "--ignored", "--exclude-standard")
	if err != nil {
		return "", ignored{}, err
	}
	var ign ignored
	var names []string
	for _, rel := range strings.Split(list, "\x00") {
		if rel == "" {
			continue
		}
		if looksLikeConfig(rel) {
			names = append(names, rel)
		}
		if info, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
			ign.files++
			ign.bytes += info.Size()
		}
	}
	if len(names) > 0 {
		more := ""
		if len(names) > 5 {
			more = ", and " + strconv.Itoa(len(names)-5) + " more"
			names = names[:5]
		}
		return "ignored files that look like config: " + strings.Join(names, ", ") + more, ignored{}, nil
	}
	return "", ign, nil
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
