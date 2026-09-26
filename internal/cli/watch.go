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
	"syscall"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

const reconnectDelay = 500 * time.Millisecond

func init() {
	commands["watch"] = cmdWatch
}

type watcher struct {
	r      *runner
	st     *state.Store
	notify bool
	seen   map[int64]string
}

func cmdWatch(r *runner, args []string) error {
	fs := flags("watch")
	notify := fs.Bool("notify", true, "send desktop notifications with notify-send")
	if _, err := parse(fs, args, 0); err != nil {
		return err
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
	w := &watcher{r: r, st: st, notify: *notify, seen: map[int64]string{}}
	for ctx.Err() == nil {
		if err := w.session(ctx); err != nil && ctx.Err() == nil {
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
	stream, err := c.Subscribe(ctx)
	if err != nil {
		return runtimeErr(err)
	}
	defer stream.Close()
	if err := w.reconcile(ctx, c, caps); err != nil {
		return err
	}
	for {
		ev, err := stream.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("event stream closed; reconnecting")
		}
		if err != nil {
			return err
		}
		switch ev.Event {
		case "pane.agent_status_changed":
			err = w.agentStatus(ctx, c, caps, ev.Data)
		case "terminal.exited", "pane.closed":
			err = w.reconcile(ctx, c, caps)
		case "events.resync_required":
			return errors.New("event stream overflowed; reconnecting")
		}
		if err != nil {
			return err
		}
	}
}

func (w *watcher) reconcile(ctx context.Context, c luvus.Client, caps luvus.Capabilities) error {
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
			w.alert(state.AttemptRef(a.ID) + " " + now.Status + ": " + now.Reason)
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
		var kind, detail string
		switch ev.Status {
		case "blocked":
			kind = "blocked"
			if ag, err := c.Explain(ctx, a.PaneID); err == nil {
				detail = ag.Hint
			}
		case "done":
			kind, detail = "quiet", "turn ended"
		default:
			return nil
		}
		if err := w.st.NoteAttempt(ctx, a.ID, kind, detail); err != nil {
			return err
		}
		w.alert(state.AttemptRef(a.ID) + " " + kind + ": " + detail)
		return nil
	}
	return nil
}

func (w *watcher) alert(text string) {
	w.say(text)
	if !w.notify {
		return
	}
	bin, err := harness.LookPath("notify-send", w.r.env.Getenv("PATH"))
	if err != nil {
		return
	}
	_ = exec.Command(bin, "--app-name=hand", "hand", text).Run()
}

func (w *watcher) say(text string) {
	var d toon.Doc
	d.Field("observed", text)
	_ = w.r.print(&d)
}
