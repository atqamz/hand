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
	"sync"
	"syscall"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
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
	pending sync.WaitGroup
}

func cmdWatch(r *runner, args []string) error {
	fs := flags("watch")
	notify := fs.Bool("notify", true, "send desktop notifications with notify-send")
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
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("%w: another hand watch is running for %s", state.ErrConflict, r.home)
	}
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
	return nil
}

func (w *watcher) agentStatus(ctx context.Context, c luvus.Client, caps luvus.Capabilities, data json.RawMessage) error {
	var ev struct {
		Pane   string `json:"pane"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return err
	}
	live, err := w.st.LiveAttempts(ctx)
	if err != nil {
		return err
	}
	for _, a := range live {
		if a.Status != state.AttemptRunning || a.PaneID != ev.Pane || a.ServerGeneration != caps.ServerGeneration {
			continue
		}
		if w.seen[a.ID] == ev.Status {
			return nil
		}
		w.seen[a.ID] = ev.Status
		ref := state.AttemptRef(a.ID)
		switch ev.Status {
		case "blocked":
			detail := ""
			if ag, err := c.Explain(ctx, a.PaneID); err == nil {
				detail = ag.Hint
			}
			if err := w.st.NoteAttempt(ctx, a.ID, "blocked", detail); err != nil {
				return err
			}
			w.alert(ctx, ref+" blocked: "+detail)
		case "done":
			detail, err := w.st.RecordQuiet(ctx, a.ID)
			if err != nil {
				return err
			}
			w.alert(ctx, ref+" quiet: "+detail)
		}
		return nil
	}
	return nil
}

func (w *watcher) alert(ctx context.Context, text string) {
	w.say(text)
	if !w.notify {
		return
	}
	bin, err := harness.LookPath("notify-send", w.r.env.Getenv("PATH"))
	if err != nil {
		return
	}
	w.pending.Add(1)
	go func() {
		defer w.pending.Done()
		nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notifyTimeout)
		defer cancel()
		_ = exec.CommandContext(nctx, bin, "--app-name=hand", w.r.fleet.Name, text).Run()
	}()
}

func (w *watcher) say(text string) {
	var d toon.Doc
	d.Field("observed", text)
	_ = w.r.print(&d)
}
