package cli

import (
	"context"
	"strconv"
	"time"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

var wakeKinds = []string{"attempt.blocked", "attempt.quiet", "attempt.reported", "attempt.exited", "attempt.interrupted", "attempt.failed", "decision.answered"}

const waitPoll = 250 * time.Millisecond

func init() {
	commands["wait"] = cmdWait
}

func cmdWait(r *runner, args []string) error {
	fs := flags("wait")
	after := fs.Int64("after", -1, "event cursor printed by `hand orient` or the previous wait")
	timeout := fs.Duration("timeout", 0, "give up after this long; 0 waits until an event arrives")
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	if *timeout < 0 {
		return usageError{"wait: --timeout must not be negative"}
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := r.ctx()
	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}
	query := context.WithoutCancel(ctx)
	cursor := *after
	if cursor < 0 {
		if cursor, err = st.LastEventSeq(query); err != nil {
			return err
		}
	}
	for {
		events, err := st.EventsAfter(query, cursor, wakeKinds, 50)
		if err != nil {
			return err
		}
		if len(events) > 0 {
			return r.printWake(events, events[len(events)-1].Seq)
		}
		select {
		case <-ctx.Done():
			return r.printWake(nil, cursor)
		case <-time.After(waitPoll):
		}
	}
}

func (r *runner) printWake(events []state.Event, cursor int64) error {
	rows := make([][]string, 0, len(events))
	for _, e := range events {
		task := ""
		if e.TaskID != 0 {
			task = state.TaskRef(e.TaskID)
		}
		rows = append(rows, []string{strconv.FormatInt(e.Seq, 10), e.Kind, task, e.Detail})
	}
	c := strconv.FormatInt(cursor, 10)
	var d toon.Doc
	d.Rows("events", []string{"seq", "kind", "task", "detail"}, rows)
	d.Field("cursor", c)
	d.Help("Wait again: `hand wait --after " + c + "`")
	return r.print(&d)
}
