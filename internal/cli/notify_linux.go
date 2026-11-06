package cli

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/proc"
)

const clickAction = "default"

var (
	clickTimeout = 10 * time.Minute
	clickLimit   = 8
)

func notifyArgv(fleet, text string) (string, []string) {
	return "notify-send", []string{"--app-name=hand", "--", fleet, text}
}

func notifyClickArgv(fleet, text string) []string {
	return []string{"--app-name=hand", "--action=default=Open", "--wait", "--", fleet, text}
}

func (w *watcher) notifyClick(ctx context.Context, bin, fleet, text, ref string) bool {
	if !w.clickSupport(bin) || !w.reserveClick() {
		return false
	}
	defer w.clicks.Add(-1)
	url := w.boardURL(ctx, ref)
	if url == "" {
		return false
	}
	nctx, cancel := context.WithTimeout(w.clickCtx, clickTimeout)
	defer cancel()
	cmd := exec.CommandContext(nctx, bin, notifyClickArgv(fleet, text)...)
	cmd.Env = w.r.env.Environ()
	proc.KillGroupOnCancel(cmd)
	proc.NoWindow(cmd)
	out, err := cmd.Output()
	if strings.TrimSpace(string(out)) == clickAction {
		w.openBoard(url)
		return true
	}
	return err == nil || nctx.Err() != nil
}

func (w *watcher) clickSupport(bin string) bool {
	w.clickOnce.Do(func() {
		ctx, cancel := context.WithTimeout(w.clickCtx, notifyTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "--help")
		cmd.Env = w.r.env.Environ()
		proc.KillGroupOnCancel(cmd)
		proc.NoWindow(cmd)
		out, err := cmd.Output()
		w.clickOK = err == nil && strings.Contains(string(out), "--action") && strings.Contains(string(out), "--wait")
	})
	return w.clickOK
}

func (w *watcher) reserveClick() bool {
	for {
		n := w.clicks.Load()
		if n >= int32(clickLimit) {
			return false
		}
		if w.clicks.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

func (w *watcher) openBoard(url string) {
	opener := openerArgv[0]
	bin, err := harness.LookPath(opener, w.r.env.Getenv("PATH"))
	if err != nil {
		return
	}
	cmd := exec.Command(bin, append(append([]string{}, openerArgv[1:]...), url)...)
	cmd.Env = w.r.env.Environ()
	proc.Detach(cmd)
	if cmd.Start() == nil {
		go func() { _ = cmd.Wait() }()
	}
}
