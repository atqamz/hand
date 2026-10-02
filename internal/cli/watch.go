package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/atqamz/hand/internal/flock"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
	"github.com/atqamz/hand/internal/update"
)

const (
	reconnectDelay = 500 * time.Millisecond
	notifyTimeout  = 10 * time.Second
)

func init() {
	commands["watch"] = cmdWatch
}

type watcher struct {
	r       *runner
	st      *state.Store
	notify  bool
	every   time.Duration
	seen    map[int64]string
	limited string
	pending sync.WaitGroup
}

func cmdWatch(r *runner, args []string) error {
	fs := flags("watch")
	notify := fs.Bool("notify", true, "send desktop notifications (notify-send on Linux, osascript on macOS)")
	every := fs.Duration("every", 30*time.Second, "reconcile attempts at least this often")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	if *every <= 0 {
		return usageError{"watch: --every must be positive"}
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	lock, err := os.OpenFile(filepath.Join(r.home, "watch.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	for try := 1; ; try++ {
		ok, err := flock.Lock(lock, false)
		if err != nil {
			return err
		}
		if ok {
			break
		}
		if try == 5 {
			return fmt.Errorf("%w: another hand watch is running for %s", state.ErrConflict, r.home)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer r.writePID(filepath.Join(r.home, "watch.pid"))()
	ctx, stop := signal.NotifyContext(r.ctx(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	w := &watcher{r: r, st: st, notify: *notify, every: *every}
	defer w.pending.Wait()
	for ctx.Err() == nil {
		err := w.session(ctx)
		if errors.Is(err, luvus.ErrIncompatible) {
			return err
		}
		if errors.Is(err, errHomeMoved) {
			return fmt.Errorf("%w; restart hand watch from the fleet's new place", err)
		}
		if err != nil && ctx.Err() == nil {
			w.say("luvus: " + err.Error())
		}
		select {
		case <-ctx.Done():
		case <-time.After(reconnectDelay):
		}
	}
	return nil
}

func (r *runner) writePID(path string) func() {
	forget, err := update.WritePID(path)
	if err != nil {
		fmt.Fprintf(r.env.Stderr, "warning: cannot write %s: %v; hand update on Windows will not stop this process\n", path, err)
		return func() {}
	}
	return forget
}

func (w *watcher) session(ctx context.Context) error {
	c, caps, err := w.r.luvus(ctx)
	if err != nil {
		return err
	}
	if !slices.Contains(caps.Methods, "events.subscribe") {
		return fmt.Errorf("%w: server lacks method events.subscribe", luvus.ErrIncompatible)
	}
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := c.Subscribe(sctx)
	if err != nil {
		return runtimeErr(err)
	}
	defer stream.Close()
	w.seen = map[int64]string{}
	if err := w.reconcile(sctx, c, caps); err != nil {
		return err
	}
	if err := w.catchUp(sctx, c, caps); err != nil {
		return err
	}
	w.autoresume(sctx, c, caps)
	events := make(chan luvus.Event)
	failed := make(chan error, 1)
	go func() {
		for {
			ev, err := stream.Next()
			if err != nil {
				failed <- err
				return
			}
			select {
			case events <- ev:
			case <-sctx.Done():
				return
			}
		}
	}()
	tick := time.NewTicker(w.every)
	defer tick.Stop()
	for {
		select {
		case <-sctx.Done():
			return sctx.Err()
		case err := <-failed:
			if errors.Is(err, io.EOF) {
				return errors.New("event stream closed; reconnecting")
			}
			return err
		case <-tick.C:
			err = w.reconcile(sctx, c, caps)
		case ev := <-events:
			err = w.handle(sctx, c, caps, ev)
		}
		if err != nil {
			return err
		}
	}
}

func (w *watcher) handle(ctx context.Context, c luvus.Client, caps luvus.Capabilities, ev luvus.Event) error {
	if err := w.r.stillHome(); err != nil {
		return err
	}
	switch ev.Event {
	case "pane.agent_status_changed":
		return w.agentStatus(ctx, c, caps, ev.Data)
	case "terminal.exited", "pane.closed":
		return w.reconcile(ctx, c, caps)
	case "events.resync_required":
		return errors.New("event stream overflowed; reconnecting")
	}
	return nil
}

func (w *watcher) reconcile(ctx context.Context, c luvus.Client, caps luvus.Capabilities) error {
	if err := w.r.stillHome(); err != nil {
		return err
	}
	before, err := w.st.LiveAttempts(ctx)
	if err != nil {
		return err
	}
	if err := w.r.sync(ctx, w.st, c, caps); err != nil {
		return err
	}
	for _, a := range before {
		now, err := w.st.Attempt(ctx, a.ID)
		if err != nil {
			return err
		}
		if !now.Live() {
			delete(w.seen, a.ID)
			w.alert(ctx, state.AttemptRef(a.ID)+" "+now.Status+": "+now.Reason)
		}
	}
	w.deliver(ctx, c, caps)
	return nil
}

func (w *watcher) deliver(ctx context.Context, c luvus.Client, caps luvus.Capabilities) {
	if w.limited != "" && w.limitCleared(ctx, c) {
		w.limited = ""
	}
	why, err := w.r.deliver(ctx, w.st, c, caps, false)
	if err != nil {
		w.say("supervisor delivery: " + err.Error())
	}
	if strings.HasPrefix(why, supervisorLimited) && why != w.limited {
		w.limited = why
		w.alert(ctx, why)
	}
}

func (w *watcher) limitCleared(ctx context.Context, c luvus.Client) bool {
	sup, ok, err := w.st.LiveSupervisor(ctx)
	if err != nil || !ok {
		return err == nil
	}
	line, err := supervisorLimit(ctx, c, sup)
	return err == nil && line == ""
}

func (w *watcher) autoresume(ctx context.Context, c luvus.Client, caps luvus.Capabilities) {
	p, err := harness.LoadPolicy(w.r.home)
	if err != nil {
		w.say("autoresume: " + err.Error())
		return
	}
	if !p.Supervisor.Autoresume {
		return
	}
	sup, err := w.r.resumeSupervisor(ctx, w.st, c, true)
	switch {
	case err != nil:
		w.say("autoresume: " + err.Error())
	case sup.ID != 0:
		w.say("supervisor " + state.SupervisorRef(sup.ID) + " resumed")
		w.deliver(ctx, c, caps)
	}
}

func (w *watcher) agentStatus(ctx context.Context, c luvus.Client, caps luvus.Capabilities, data json.RawMessage) error {
	var ev struct {
		Pane   string `json:"pane"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return err
	}
	sup, ok, err := w.st.LiveSupervisor(ctx)
	if err != nil {
		return err
	}
	if ok && sup.Status == state.AttemptRunning && sup.PaneID == ev.Pane && sup.ServerGeneration == caps.ServerGeneration {
		w.deliver(ctx, c, caps)
		return nil
	}
	live, err := w.st.LiveAttempts(ctx)
	if err != nil {
		return err
	}
	for _, a := range live {
		if a.Status == state.AttemptRunning && a.PaneID == ev.Pane && a.ServerGeneration == caps.ServerGeneration {
			return w.observe(ctx, c, a, ev.Status)
		}
	}
	return nil
}

func (w *watcher) catchUp(ctx context.Context, c luvus.Client, caps luvus.Capabilities) error {
	live, err := w.st.LiveAttempts(ctx)
	if err != nil {
		return err
	}
	for _, a := range live {
		if a.Status != state.AttemptRunning || a.ServerGeneration != caps.ServerGeneration {
			continue
		}
		ag, err := c.Explain(ctx, a.PaneID)
		if err != nil {
			continue
		}
		last, err := w.st.LastAttemptEvent(ctx, a.ID)
		if err != nil {
			return err
		}
		prev := "working"
		switch {
		case (last == "attempt.quiet" || last == "attempt.idle" || last == "attempt.limited") && ag.Status != "blocked":
			prev = ag.Status
		case last == "attempt.blocked":
			prev = "blocked"
		case ag.Status == "idle" && last != "attempt.keys" && last != "attempt.sent":
			prev = "idle"
		}
		w.seen[a.ID] = prev
		if err := w.observe(ctx, c, a, ag.Status); err != nil {
			return err
		}
	}
	return nil
}

func (w *watcher) observe(ctx context.Context, c luvus.Client, a state.Attempt, status string) error {
	prev := w.seen[a.ID]
	if prev == status {
		return nil
	}
	w.seen[a.ID] = status
	ref := state.AttemptRef(a.ID)
	switch status {
	case "blocked":
		detail := ""
		if ag, err := c.Explain(ctx, a.PaneID); err == nil {
			detail = ag.Hint
		}
		if err := w.st.NoteAttempt(ctx, a.ID, "blocked", detail); err != nil {
			return err
		}
		w.alert(ctx, ref+" blocked: "+detail)
	case "done", "idle":
		if status == "idle" && prev != "working" && prev != "blocked" {
			return nil
		}
		s, err := c.Read(ctx, a.PaneID, luvus.ScreenLines)
		if err != nil {
			return err
		}
		if line, ok := harness.Limit(a.Harness, s.Text); ok {
			seen, err := w.st.LastLimit(ctx, a.ID)
			if err != nil {
				return err
			}
			if shown(seen) != shown(line) {
				if err := w.st.NoteAttempt(ctx, a.ID, "limited", line); err != nil {
					return err
				}
				w.alert(ctx, ref+" limited: "+line)
				return nil
			}
		}
		detail, err := w.st.RecordQuiet(ctx, a.ID)
		if err != nil {
			return err
		}
		w.alert(ctx, ref+" quiet: "+detail)
	}
	return nil
}

func shown(line string) string {
	line, _, _ = strings.Cut(line, " (~")
	return line
}

func (w *watcher) alert(ctx context.Context, text string) {
	w.say(text)
	if !w.notify {
		return
	}
	name, args := notifyArgv(w.r.fleet.Name, text)
	bin, err := harness.LookPath(name, w.r.env.Getenv("PATH"))
	if err != nil {
		return
	}
	w.pending.Add(1)
	go func() {
		defer w.pending.Done()
		nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notifyTimeout)
		defer cancel()
		_ = exec.CommandContext(nctx, bin, args...).Run()
	}()
}

func (w *watcher) say(text string) {
	var d toon.Doc
	d.Field("observed", text)
	_ = w.r.print(&d)
}
