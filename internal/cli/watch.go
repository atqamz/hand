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
	"strconv"
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

const longTurn = 60 * time.Minute

const longDetail = "working for 60m"

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
	limited int64
	turns   map[int64]*turn
	asked   int64
	pending sync.WaitGroup
}

type turn struct {
	since time.Time
	long  bool
}

func cmdWatch(r *runner, args []string) error {
	fs := flags("watch")
	notify := fs.Bool("notify", true, "send notifications: the routing.json notify command, else notify-send on Linux or osascript on macOS")
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
	defer flock.Release(lock)
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
	r.exits = &exitLog{m: map[string]exitRecord{}}
	w := &watcher{r: r, st: st, notify: *notify, every: *every, turns: map[int64]*turn{}}
	if w.asked, err = st.LastEventSeq(ctx); err != nil {
		return err
	}
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
	events := make(chan luvus.Event, 64)
	failed := make(chan error, 1)
	go func() {
		for {
			ev, err := stream.Next()
			if err != nil {
				failed <- err
				return
			}
			if ev.Event == "terminal.exited" {
				w.r.exits.note(ev.Data)
			}
			select {
			case events <- ev:
			case <-sctx.Done():
				return
			}
		}
	}()
	w.seen = map[int64]string{}
	if err := w.reconcile(sctx, c, caps); err != nil {
		return err
	}
	if err := w.catchUp(sctx, c, caps); err != nil {
		return err
	}
	w.autoresume(sctx, c, caps)
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
			if err = w.reconcile(sctx, c, caps); err == nil {
				err = w.longTurns(sctx)
			}
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

type exitRecord struct {
	reason string
	at     time.Time
}

const exitKeep = 10 * time.Minute

type exitLog struct {
	mu       sync.Mutex
	m        map[string]exitRecord
	deadline time.Time
}

const exitWait = time.Second

func (l *exitLog) budget() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.deadline = time.Now().Add(exitWait)
}

func (l *exitLog) wait(ctx context.Context, id string) string {
	for {
		l.mu.Lock()
		rec, ok := l.m[id]
		deadline := l.deadline
		l.mu.Unlock()
		if ok && time.Since(rec.at) <= exitKeep {
			return rec.reason
		}
		left := time.Until(deadline)
		if left <= 0 {
			return "terminal exited"
		}
		timer := time.NewTimer(min(left, 10*time.Millisecond))
		select {
		case <-ctx.Done():
			timer.Stop()
			return "terminal exited"
		case <-timer.C:
		}
	}
}

func (l *exitLog) note(data json.RawMessage) {
	var ev struct {
		TerminalID string `json:"terminal_id"`
		Detail     struct {
			ExitCode *int    `json:"exit_code"`
			Signal   *string `json:"signal"`
		} `json:"detail"`
	}
	if json.Unmarshal(data, &ev) != nil || ev.TerminalID == "" {
		return
	}
	var reason string
	switch {
	case ev.Detail.Signal != nil && *ev.Detail.Signal != "":
		reason = "terminal exited (signal " + *ev.Detail.Signal + ")"
	case ev.Detail.ExitCode != nil:
		reason = "terminal exited (code " + strconv.Itoa(*ev.Detail.ExitCode) + ")"
	default:
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for id, rec := range l.m {
		if now.Sub(rec.at) > exitKeep {
			delete(l.m, id)
		}
	}
	l.m[ev.TerminalID] = exitRecord{reason, now}
}

func (w *watcher) reconcile(ctx context.Context, c luvus.Client, caps luvus.Capabilities) error {
	if err := w.r.stillHome(); err != nil {
		return err
	}
	w.r.exits.budget()
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
			ref := state.AttemptRef(a.ID)
			w.alert(ctx, "attempt."+now.Status, ref, ref+" "+now.Status+": "+now.Reason)
		}
	}
	if err := w.decisions(ctx); err != nil {
		return err
	}
	w.deliver(ctx, c, caps)
	return nil
}

func (w *watcher) decisions(ctx context.Context) error {
	events, err := w.st.EventsAfter(ctx, w.asked, []string{"decision.asked"}, wakeBatch)
	if err != nil {
		return err
	}
	for _, e := range events {
		w.asked = e.Seq
		id, err := parseID("d", e.Detail)
		if err != nil {
			w.say("decision alert: " + err.Error())
			continue
		}
		d, err := w.st.Decision(ctx, id)
		if err != nil {
			w.say("decision alert: " + err.Error())
			continue
		}
		w.alert(ctx, e.Kind, e.Detail, state.TaskRef(e.TaskID)+" decision: "+d.Headline())
	}
	return nil
}

func (w *watcher) deliver(ctx context.Context, c luvus.Client, caps luvus.Capabilities) {
	if w.limited != 0 && w.limitCleared(ctx, c) {
		w.limited = 0
	}
	why, err := w.r.deliver(ctx, w.st, c, caps, false)
	if err != nil {
		w.say("supervisor delivery: " + err.Error())
	}
	if !strings.HasPrefix(why, supervisorLimited) {
		return
	}
	if sup, ok, err := w.st.LiveSupervisor(ctx); err == nil && ok && sup.ID != w.limited {
		w.limited = sup.ID
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
		case last == "attempt.long":
		case ag.Status == "idle" && last != "attempt.keys" && last != "attempt.sent":
			prev = "idle"
		}
		w.seen[a.ID] = prev
		if ag.Status == "working" && w.turns[a.ID] == nil {
			since, long, err := w.st.TurnStart(ctx, a.ID)
			if err != nil {
				return err
			}
			if since.IsZero() {
				since = w.r.env.Now()
			}
			w.turns[a.ID] = &turn{since: since, long: long}
		}
		if err := w.observe(ctx, c, a, ag.Status); err != nil {
			return err
		}
	}
	return nil
}

func (w *watcher) longTurns(ctx context.Context) error {
	live, err := w.st.LiveAttempts(ctx)
	if err != nil {
		return err
	}
	running := map[int64]bool{}
	for _, a := range live {
		running[a.ID] = a.Status == state.AttemptRunning
	}
	for id, t := range w.turns {
		if !running[id] {
			delete(w.turns, id)
			continue
		}
		if t.long || w.r.env.Now().Sub(t.since) < longTurn {
			continue
		}
		if err := w.st.NoteAttempt(ctx, id, "long", longDetail); err != nil {
			return err
		}
		t.long = true
		w.alert(ctx, state.AttemptRef(id)+" long: "+longDetail)
	}
	return nil
}

func (w *watcher) observe(ctx context.Context, c luvus.Client, a state.Attempt, status string) error {
	switch {
	case status != "working":
		delete(w.turns, a.ID)
	case w.turns[a.ID] == nil:
		w.turns[a.ID] = &turn{since: w.r.env.Now()}
	}
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
		w.alert(ctx, "attempt.blocked", ref, ref+" blocked: "+detail)
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
				w.alert(ctx, "attempt.limited", ref, ref+" limited: "+line)
				return nil
			}
		}
		detail, err := w.st.RecordQuiet(ctx, a.ID)
		if err != nil {
			return err
		}
		w.alert(ctx, "attempt.quiet", ref, ref+" quiet: "+detail)
	}
	return nil
}

func shown(line string) string {
	line, _, _ = strings.Cut(line, " (~")
	return line
}

func (w *watcher) alert(ctx context.Context, kind, ref, text string) {
	w.say(text)
	if !w.notify {
		return
	}
	name, args, extra := w.notifier(ctx, kind, ref, text)
	bin, err := harness.LookPath(name, w.r.env.Getenv("PATH"))
	if err != nil {
		if extra != nil {
			w.say("notify: " + err.Error())
		}
		return
	}
	env := append(w.r.env.Environ(), extra...)
	w.pending.Add(1)
	go func() {
		defer w.pending.Done()
		nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notifyTimeout)
		defer cancel()
		cmd := exec.CommandContext(nctx, bin, args...)
		cmd.Env = env
		_ = cmd.Run()
	}()
}

func (w *watcher) notifier(ctx context.Context, kind, ref, text string) (string, []string, []string) {
	if p, err := harness.LoadPolicy(w.r.home); err == nil && len(p.Notify) > 0 {
		return p.Notify[0], p.Notify[1:], []string{"HAND_NOTIFY_KIND=" + kind, "HAND_NOTIFY_REF=" + ref, "HAND_NOTIFY_TEXT=" + text, "HAND_NOTIFY_URL=" + w.boardURL(ctx, ref)}
	}
	name, args := notifyArgv(w.r.fleet.Name, text)
	return name, args, nil
}

func (w *watcher) boardURL(ctx context.Context, ref string) string {
	addr, err := os.ReadFile(filepath.Join(w.r.root, "board.addr"))
	if err != nil {
		return ""
	}
	path, fragment, err := boardPage(ctx, w.st, ref)
	if err != nil {
		return ""
	}
	return "http://" + strings.TrimSpace(string(addr)) + "/" + w.r.fleet.ID + path + fragment
}

func (w *watcher) say(text string) {
	var d toon.Doc
	d.Field("observed", text)
	_ = w.r.print(&d)
}
