package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/inbox"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

const wakeUsage = "usage: hand wake --after DURATION | --at TIME | --every DURATION [--while tN] [--waits] [--note TEXT]; hand wake list; hand wake cancel wN. A timer counts as watching only when it is due within 6 hours; --waits (needs --while tN, one-shot, at most 30d away) marks tN as waiting on an outside party until it fires"

var wakeCommands = map[string]handler{
	"list":   cmdWakeList,
	"cancel": cmdWakeCancel,
}

func init() {
	commands["wake"] = cmdWake
}

func timerDoc(t state.Timer) *toon.Doc {
	var d toon.Doc
	d.Field("timer", state.TimerRef(t.ID))
	d.Field("status", t.Status)
	d.Field("due", t.Due.Format(time.RFC3339))
	if t.Every > 0 {
		d.Field("every", state.Span(t.Every))
	}
	if t.TaskID != 0 {
		d.Field("while", state.TaskRef(t.TaskID))
	}
	if t.Waits {
		d.Field("waits", "yes")
	}
	if t.Note != "" {
		d.Field("note", t.Note)
	}
	return &d
}

func spanFlag(fs *flag.FlagSet, name, usage string) *time.Duration {
	d := new(time.Duration)
	fs.Func(name, usage, func(s string) (err error) {
		if days, ok := inbox.ParseAge(s); ok {
			*d = days
			return nil
		}
		*d, err = time.ParseDuration(s)
		return err
	})
	return d
}

func cmdWake(r *runner, args []string) error {
	if len(args) > 0 {
		if h, ok := wakeCommands[args[0]]; ok {
			return h(r, args[1:])
		}
	}
	if len(args) == 0 || !strings.HasPrefix(args[0], "-") {
		return usageError{wakeUsage}
	}
	fs := flags("wake")
	after := spanFlag(fs, "after", "wake the supervisor once, this long from now, like 30m or 3d")
	at := fs.String("at", "", "wake the supervisor once at this RFC 3339 time")
	every := spanFlag(fs, "every", "wake the supervisor this often, like 30m or 1d")
	while := fs.String("while", "", "cancel the timer when this task is done or abandoned")
	waits := fs.Bool("waits", false, "mark the --while task as waiting on an outside party until the timer fires")
	note := fs.String("note", "", "what to check when it fires, or why the task waits, at most 200 characters")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	var when []string
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "after" || f.Name == "at" || f.Name == "every" {
			when = append(when, f.Name)
		}
	})
	if len(when) != 1 {
		return usageError{"wake: give exactly one of --after, --at or --every"}
	}
	spec := state.TimerSpec{After: *after, Every: *every, Note: *note, Waits: *waits}
	if when[0] == "at" {
		t, err := time.Parse(time.RFC3339, *at)
		if err != nil {
			return usageError{fmt.Sprintf("wake: --at %q is not an RFC 3339 time like YYYY-MM-DDTHH:MM:SSZ", *at)}
		}
		spec.At = t
	}
	if *while != "" {
		id, err := parseID("t", *while)
		if err != nil {
			return err
		}
		spec.TaskID = id
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	managed, err := r.managedSupervisor(ctx, st)
	if err != nil {
		return err
	}
	if !managed {
		return fmt.Errorf("%w: timers fire only while a managed supervisor runs, and none does: start one with `hand supervisor start`; a supervisor opened by hand waits with `hand wait --timeout` instead", state.ErrConflict)
	}
	t, err := st.AddTimer(ctx, spec)
	if err != nil {
		return err
	}
	return r.print(timerDoc(t))
}

func cmdWakeList(r *runner, args []string) error {
	if _, err := parse(flags("wake list"), args, 0); err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	timers, err := st.Timers(context.Background())
	if err != nil {
		return err
	}
	rows := make([][]string, 0, len(timers))
	for _, t := range timers {
		every, while := "", ""
		if t.Every > 0 {
			every = state.Span(t.Every)
		}
		if t.TaskID != 0 {
			while = state.TaskRef(t.TaskID)
		}
		rows = append(rows, []string{state.TimerRef(t.ID), t.Due.Format(time.RFC3339), every, while, t.Note})
	}
	var d toon.Doc
	d.Rows("timers", []string{"id", "due", "every", "while", "note"}, rows)
	return r.print(&d)
}

func cmdWakeCancel(r *runner, args []string) error {
	pos, err := parse(flags("wake cancel"), args, 1)
	if err != nil {
		return err
	}
	id, err := parseID("w", pos[0])
	if err != nil {
		return err
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	t, err := st.CancelTimer(context.Background(), id)
	if err != nil {
		return err
	}
	return r.print(timerDoc(t))
}
