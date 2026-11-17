package cli_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/state"
)

type interruptFixture struct {
	*attemptFixture
	file string
	n    int
}

func newInterruptFixture(t *testing.T, routing string) *interruptFixture {
	t.Helper()
	h, rt := newSupervisorFixture(t)
	if routing != "" {
		if err := os.WriteFile(filepath.Join(h.home, "routing.json"), []byte(routing), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	startClaudeSupervisor(h)
	dir := filepath.Join(h.vars["HOME"], ".claude", "projects", "p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, rt.lastCreate().Command[3]+".jsonl")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	rt.set(func(rt *fakeRuntime) {
		rt.status, rt.screen, rt.afterKeys = "working", "idle prompt\n❯ \nfooter", "idle"
	})
	setWait(t, cli.PreemptEvery, 10*time.Millisecond)
	setWait(t, cli.InterruptPoll, 10*time.Millisecond)
	setWait(t, cli.InterruptSettle, 200*time.Millisecond)
	return &interruptFixture{attemptFixture: &attemptFixture{h: h, rt: rt}, file: file}
}

func (f *interruptFixture) record(lines ...string) {
	f.h.t.Helper()
	fh, err := os.OpenFile(f.file, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		f.h.t.Fatal(err)
	}
	defer fh.Close()
	for _, l := range lines {
		fmt.Fprintln(fh, l)
	}
}

func (f *interruptFixture) running(command string) {
	f.n++
	f.record(fmt.Sprintf(`{"type":"assistant","timestamp":"2026-09-26T00:00:30.000Z","message":{"id":"m%d","role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"u%d","name":"Bash","input":{"command":%q}}]}}`, f.n, f.n, command))
}

func (f *interruptFixture) finished() {
	f.record(fmt.Sprintf(`{"type":"user","timestamp":"2026-09-26T00:01:30.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"u%d","content":"ok"}]}}`, f.n))
}

func (f *interruptFixture) input(t *testing.T, id int64) state.SupervisorInput {
	t.Helper()
	return inputState(t, f.h, id)
}

func (f *interruptFixture) events(t *testing.T, kind string) []string {
	t.Helper()
	events, err := openStore(t, f.h).RecentEventsOf(context.Background(), []string{kind}, 50)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range events {
		out = append(out, e.Detail)
	}
	return out
}

func (f *interruptFixture) pastTheBound() { f.h.advance(61 * time.Second) }

func (f *interruptFixture) quiet(t *testing.T) {
	t.Helper()
	time.Sleep(300 * time.Millisecond)
}

func TestAMessageWaitingPastTheBoundDuringAKnownWaitInterruptsOnceAndIsTypedAgain(t *testing.T) {
	f := newInterruptFixture(t, "")
	f.h.ok("supervisor", "send", "--text", "status?")
	f.running("for i in 1 2 3; do gh pr checks 1285; sleep 150; done")
	stop := startWatch(t, f.attemptFixture, "--every", "1h")
	f.pastTheBound()
	eventually(t, func() bool { return f.input(t, 1).Reason == state.ReasonInterrupted })
	f.quiet(t)
	stop()
	if got := f.rt.keysSent(); !slices.Equal(got, []string{"esc"}) {
		t.Fatalf("keys = %q, want one esc", got)
	}
	if got := f.rt.prompts(); !slices.Equal(got, []string{"status?", "status?"}) {
		t.Fatalf("prompts = %q, want the message typed again", got)
	}
	if in := f.input(t, 1); in.ReasonDetail != "" || in.Status() != "delivered" {
		t.Fatalf("input = %+v", in)
	}
	if ev := f.events(t, "input.interrupted"); !slices.Equal(ev, []string{"i1"}) {
		t.Fatalf("interrupted events = %q", ev)
	}
}

func TestAMessageIsNotInterruptedBeforeTheBound(t *testing.T) {
	f := newInterruptFixture(t, "")
	f.h.ok("supervisor", "send", "--text", "status?")
	f.running("sleep 300")
	startWatch(t, f.attemptFixture, "--every", "1h")
	f.h.advance(30 * time.Second)
	f.quiet(t)
	if got := f.rt.keysSent(); len(got) != 0 {
		t.Fatalf("keys before the bound = %q", got)
	}
	if in := f.input(t, 1); in.Reason != "" {
		t.Fatalf("input = %+v", in)
	}
	f.h.advance(31 * time.Second)
	eventually(t, func() bool { return f.input(t, 1).Reason == state.ReasonInterrupted })
}

func TestTheBoundIsTheSettingNotAConstant(t *testing.T) {
	f := newInterruptFixture(t, `{"supervisor":{"answer_within":"5m"}}`)
	f.h.ok("supervisor", "send", "--text", "status?")
	f.running("sleep 300")
	startWatch(t, f.attemptFixture, "--every", "1h")
	f.pastTheBound()
	f.quiet(t)
	if got := f.rt.keysSent(); len(got) != 0 {
		t.Fatalf("keys under a 5m bound after 61s = %q", got)
	}
	f.h.advance(5 * time.Minute)
	eventually(t, func() bool { return f.input(t, 1).Reason == state.ReasonInterrupted })
}

func TestAMessageDuringACommandThatIsNotAKnownWaitWaitsWithTheReason(t *testing.T) {
	f := newInterruptFixture(t, "")
	f.h.ok("supervisor", "send", "--text", "status?")
	f.running("gh pr merge 1285 --squash")
	stop := startWatch(t, f.attemptFixture, "--every", "1h")
	f.pastTheBound()
	eventually(t, func() bool { return f.input(t, 1).Reason == state.ReasonWaited })
	f.quiet(t)
	if in := f.input(t, 1); in.ReasonDetail != state.WaitNotWait || in.Status() != "delivered" {
		t.Fatalf("input = %+v", in)
	}
	if got := f.rt.keysSent(); len(got) != 0 {
		t.Fatalf("keys during a merge = %q", got)
	}
	if ev := f.events(t, "input.waited"); !slices.Equal(ev, []string{"i1: not_wait"}) {
		t.Fatalf("waited events after many sweeps = %q", ev)
	}
	f.finished()
	f.running("sleep 120")
	eventually(t, func() bool { return f.input(t, 1).Reason == state.ReasonInterrupted })
	stop()
	if got := f.rt.keysSent(); !slices.Equal(got, []string{"esc"}) {
		t.Fatalf("keys once the merge ended and a sleep began = %q", got)
	}
}

func TestAMixedCompoundCommandIsNotAWait(t *testing.T) {
	f := newInterruptFixture(t, "")
	f.h.ok("supervisor", "send", "--text", "status?")
	f.running("sleep 30 && git push origin hand/x")
	startWatch(t, f.attemptFixture, "--every", "1h")
	f.pastTheBound()
	eventually(t, func() bool { return f.input(t, 1).Reason == state.ReasonWaited })
	f.quiet(t)
	if got := f.rt.keysSent(); len(got) != 0 || f.input(t, 1).ReasonDetail != state.WaitNotWait {
		t.Fatalf("keys = %q, input = %+v", got, f.input(t, 1))
	}
}

func TestWithTheSettingOffAMessageWaitsAndSaysSo(t *testing.T) {
	f := newInterruptFixture(t, `{"supervisor":{"interrupt":false}}`)
	f.h.ok("supervisor", "send", "--text", "status?")
	f.running("sleep 300")
	startWatch(t, f.attemptFixture, "--every", "1h")
	f.pastTheBound()
	eventually(t, func() bool { return f.input(t, 1).Reason == state.ReasonWaited })
	f.quiet(t)
	if in := f.input(t, 1); in.ReasonDetail != state.WaitOff {
		t.Fatalf("input = %+v", in)
	}
	if got := f.rt.keysSent(); len(got) != 0 {
		t.Fatalf("keys with the setting off = %q", got)
	}
	if got := f.rt.prompts(); !slices.Equal(got, []string{"status?"}) {
		t.Fatalf("prompts = %q", got)
	}
}

func TestAMessageIsNeverInterruptedTwice(t *testing.T) {
	f := newInterruptFixture(t, "")
	f.h.ok("supervisor", "send", "--text", "status?")
	f.running("sleep 300")
	startWatch(t, f.attemptFixture, "--every", "1h")
	f.pastTheBound()
	eventually(t, func() bool { return f.input(t, 1).Reason == state.ReasonInterrupted })
	f.rt.set(func(rt *fakeRuntime) { rt.status = "working" })
	f.finished()
	f.running("sleep 300")
	f.h.advance(2 * time.Minute)
	f.quiet(t)
	if got := f.rt.keysSent(); !slices.Equal(got, []string{"esc"}) {
		t.Fatalf("keys after a second sweep = %q, want still one esc", got)
	}
	if ev := f.events(t, "input.interrupted"); len(ev) != 1 {
		t.Fatalf("interrupted events = %q", ev)
	}
}

func TestABlockedSupervisorIsNeverInterruptedAndTheMessageKeepsItsState(t *testing.T) {
	f := newInterruptFixture(t, "")
	f.h.ok("supervisor", "send", "--text", "status?")
	f.running("sleep 300")
	f.rt.set(func(rt *fakeRuntime) { rt.status, rt.hint = "blocked", "Do you want to proceed?" })
	startWatch(t, f.attemptFixture, "--every", "1h")
	f.pastTheBound()
	f.quiet(t)
	if got := f.rt.keysSent(); len(got) != 0 {
		t.Fatalf("keys to a blocked supervisor = %q", got)
	}
	if in := f.input(t, 1); in.Reason != "" || in.Status() != "delivered" {
		t.Fatalf("input = %+v", in)
	}
	if ev := f.events(t, "input.waited"); len(ev) != 0 {
		t.Fatalf("waited events = %q", ev)
	}
}

func TestALimitedOrIdleSupervisorIsNeverInterrupted(t *testing.T) {
	for name, set := range map[string]func(*interruptFixture){
		"limited": func(f *interruptFixture) { f.rt.set(func(rt *fakeRuntime) { rt.screen = claudeLimit }) },
		"idle":    func(f *interruptFixture) { f.rt.set(func(rt *fakeRuntime) { rt.status = "idle" }) },
		"switching": func(f *interruptFixture) {
			f.h.ok("supervisor", "switch", "--model", "opus", "--effort", "high")
			f.rt.set(func(rt *fakeRuntime) { rt.status = "working" })
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newInterruptFixture(t, "")
			f.h.ok("supervisor", "send", "--text", "status?")
			f.running("sleep 300")
			set(f)
			startWatch(t, f.attemptFixture, "--every", "1h")
			f.pastTheBound()
			f.quiet(t)
			if got := f.rt.keysSent(); len(got) != 0 {
				t.Fatalf("keys = %q", got)
			}
			if in := f.input(t, 1); in.Reason != "" {
				t.Fatalf("input = %+v", in)
			}
		})
	}
}

func TestAMessageIsNotInterruptedWhileThePaneHoldsTypedText(t *testing.T) {
	f := newInterruptFixture(t, "")
	f.h.ok("supervisor", "send", "--text", "status?")
	f.running("sleep 300")
	f.rt.set(func(rt *fakeRuntime) { rt.screen = "working\n❯ half a sentence the operator typed\nfooter" })
	startWatch(t, f.attemptFixture, "--every", "1h")
	f.pastTheBound()
	eventually(t, func() bool { return f.input(t, 1).Reason == state.ReasonWaited })
	f.quiet(t)
	if in := f.input(t, 1); in.ReasonDetail != state.WaitTyped {
		t.Fatalf("input = %+v", in)
	}
	if got := f.rt.keysSent(); len(got) != 0 {
		t.Fatalf("keys over typed text = %q", got)
	}
}

func TestAMessageIsNotInterruptedAfterTheOperatorPressedKeys(t *testing.T) {
	f := newInterruptFixture(t, "")
	f.h.ok("supervisor", "send", "--text", "status?")
	f.running("sleep 300")
	f.rt.set(func(rt *fakeRuntime) { rt.afterKeys = "" })
	f.h.advance(time.Second)
	f.h.ok("supervisor", "keys", "--revision", "0", "down")
	f.rt.set(func(rt *fakeRuntime) { rt.keyed, rt.afterKeys = nil, "idle" })
	startWatch(t, f.attemptFixture, "--every", "1h")
	f.pastTheBound()
	eventually(t, func() bool { return f.input(t, 1).Reason == state.ReasonWaited })
	f.quiet(t)
	if in := f.input(t, 1); in.ReasonDetail != state.WaitTyped || len(f.rt.keysSent()) != 0 {
		t.Fatalf("input = %+v, keys %q", in, f.rt.keysSent())
	}
}

func TestAMessageWaitsAsUnknownWhenTheRecordShowsNoRunningCall(t *testing.T) {
	for name, setup := range map[string]func(*interruptFixture){
		"a call that finished": func(f *interruptFixture) { f.running("sleep 300"); f.finished() },
		"no record at all":     func(f *interruptFixture) { _ = os.Remove(f.file) },
		"an empty record":      func(*interruptFixture) {},
	} {
		t.Run(name, func(t *testing.T) {
			f := newInterruptFixture(t, "")
			f.h.ok("supervisor", "send", "--text", "status?")
			setup(f)
			startWatch(t, f.attemptFixture, "--every", "1h")
			f.pastTheBound()
			eventually(t, func() bool { return f.input(t, 1).Reason == state.ReasonWaited })
			f.quiet(t)
			if in := f.input(t, 1); in.ReasonDetail != state.WaitUnknown {
				t.Fatalf("input = %+v", in)
			}
			if got := f.rt.keysSent(); len(got) != 0 {
				t.Fatalf("keys = %q", got)
			}
		})
	}
}

func TestAMessageTheHarnessAlreadyTookIsNotInterruptedOrRetyped(t *testing.T) {
	f := newInterruptFixture(t, "")
	f.h.ok("supervisor", "send", "--text", "status?")
	f.record(`{"type":"user","timestamp":"2026-09-26T00:00:05.000Z","message":{"role":"user","content":"status?"}}`)
	f.running("sleep 300")
	startWatch(t, f.attemptFixture, "--every", "1h")
	f.pastTheBound()
	f.quiet(t)
	if got := f.rt.keysSent(); len(got) != 0 {
		t.Fatalf("keys for a message the harness took = %q", got)
	}
	if in := f.input(t, 1); in.Reason != "" {
		t.Fatalf("input = %+v", in)
	}
}

func TestAnAnsweredMessageIsNotInterrupted(t *testing.T) {
	f := newInterruptFixture(t, "")
	f.h.ok("supervisor", "send", "--text", "status?")
	f.running("sleep 300")
	if err := openStore(t, f.h).AnswerSupervisorInput(context.Background(), 1, "2026-09-26T00:00:10Z"); err != nil {
		t.Fatal(err)
	}
	startWatch(t, f.attemptFixture, "--every", "1h")
	f.pastTheBound()
	f.quiet(t)
	if got := f.rt.keysSent(); len(got) != 0 {
		t.Fatalf("keys for an answered message = %q", got)
	}
}

func TestAHarnessWhoseRecordShowsNoRunningCallNeverInterrupts(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	h.ok("supervisor", "start", "--harness", "opencode")
	rt.set(func(rt *fakeRuntime) {
		rt.status, rt.screen, rt.afterKeys = "working", "idle prompt\n❯ \nfooter", "idle"
	})
	setWait(t, cli.PreemptEvery, 10*time.Millisecond)
	h.ok("supervisor", "send", "--text", "status?")
	f := &interruptFixture{attemptFixture: &attemptFixture{h: h, rt: rt}}
	startWatch(t, f.attemptFixture, "--every", "1h")
	h.advance(61 * time.Second)
	eventually(t, func() bool { return f.input(t, 1).Reason == state.ReasonWaited })
	f.quiet(t)
	if in := f.input(t, 1); in.ReasonDetail != state.WaitUnknown || len(rt.keysSent()) != 0 {
		t.Fatalf("input = %+v, keys %q", in, rt.keysSent())
	}
}

func TestIfTheTurnDoesNotStopTheMessageIsNotTypedAgainAndTheReceiptSaysSo(t *testing.T) {
	f := newInterruptFixture(t, "")
	f.rt.set(func(rt *fakeRuntime) { rt.afterKeys = "" })
	f.h.ok("supervisor", "send", "--text", "status?")
	f.running("sleep 300")
	startWatch(t, f.attemptFixture, "--every", "1h")
	f.pastTheBound()
	eventually(t, func() bool { return f.input(t, 1).Reason == state.ReasonInterrupted })
	in := f.input(t, 1)
	if !strings.HasPrefix(in.ReasonDetail, "not sent again: ") {
		t.Fatalf("input = %+v", in)
	}
	if got := f.rt.prompts(); !slices.Equal(got, []string{"status?"}) {
		t.Fatalf("prompts = %q, want no second typing into a turn that did not stop", got)
	}
}
