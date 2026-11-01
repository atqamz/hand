package cli_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
)

func runningWorker(t *testing.T, extra map[string]string) (*attemptFixture, *opencodeRig, string) {
	t.Helper()
	fx := newAttemptFixture(t)
	params := map[string]string{"models": "local/m1\nlocal/m2\n", "sessionmodel": `{"id":"m1","providerID":"local"}`, "sessionlist": filepath.Join(t.TempDir(), "sessions"), "swapply": filepath.Join(t.TempDir(), "applied")}
	for k, v := range extra {
		params[k] = v
	}
	o := installOpencode(t, fx.h, fx.rt, params)
	fx.rt.srv.Handle("terminal.backend.close", func(p json.RawMessage) (any, error) {
		fakebin.Append(o.log, "stop")
		return fx.rt.close(p)
	})
	fx.h.ok("attempt", "start", "--harness", "opencode", "--model", "local/m1", "--prompt-file", fx.brief, "t1")
	argv := fx.rt.lastCreate().Command
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	return fx, o, argv[slices.Index(argv, "--session")+1]
}

func fieldOf(doc, name string) string {
	for _, line := range strings.Split(doc, "\n") {
		if v, ok := strings.CutPrefix(line, name+": "); ok {
			return v
		}
	}
	return ""
}

func attemptSwitchedEvents(t *testing.T, fx *attemptFixture) []string {
	t.Helper()
	events, err := openStore(t, fx.h).EventsAfter(context.Background(), 0, []string{"attempt.switched"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range events {
		out = append(out, e.Detail)
	}
	return out
}

func TestAttemptSwitchRestartsThePaneOnTheSameSessionWithNoBrief(t *testing.T) {
	fx, o, session := runningWorker(t, nil)
	before := fx.h.ok("attempt", "show", "a1")
	out := fx.h.ok("attempt", "switch", "a1", "--model", "local/m2", "--effort", "deep")
	has(t, "switch", out, "attempt: a1", "status: running", "model: local/m2", "effort: deep", "background shells")
	want := []string{"stop", "switch " + session + ` {"model":{"providerID":"local","id":"m2","variant":"deep"}}`, "pane"}
	if got := afterStart(o, 2); !slices.Equal(got, want) {
		t.Fatalf("events after the start = %q, want %q", got, want)
	}
	argv := fx.rt.lastCreate().Command
	if !slices.Equal(argv[1:], []string{"--standalone", "--auto", "--session", session}) {
		t.Fatalf("relaunch argv = %q", argv)
	}
	if got := attemptSwitchedEvents(t, fx); len(got) != 1 || !strings.HasPrefix(got[0], "a1: switched to local/m2 deep, pane ") {
		t.Fatalf("attempt.switched events = %q", got)
	}
	after := fx.h.ok("attempt", "show", "a1")
	has(t, "show", after, "status: running")
	if pane := fieldOf(before, "pane"); pane == "" || pane == fieldOf(after, "pane") {
		t.Fatalf("the pane did not change: %q, %q", before, after)
	}
	for _, line := range strings.Split(before, "\n") {
		if strings.HasPrefix(line, "prompt_bytes") || strings.HasPrefix(line, "launch:") {
			has(t, "kept brief", after, line)
		}
	}
}

func TestAttemptSwitchWithoutAVariantSendsNone(t *testing.T) {
	fx, o, session := runningWorker(t, nil)
	out := fx.h.ok("attempt", "switch", "--model", "local/m2", "a1")
	if strings.Contains(out, "effort:") {
		t.Fatalf("switch = %q", out)
	}
	if got := afterStart(o, 2); len(got) != 3 || got[1] != "switch "+session+` {"model":{"providerID":"local","id":"m2"}}` {
		t.Fatalf("events after the start = %q", got)
	}
}

func TestAttemptSwitchRefusesWithoutTouchingAnything(t *testing.T) {
	for name, tc := range map[string]struct {
		extra map[string]string
		args  []string
		setup func(*fakeRuntime)
		code  int
		want  string
	}{
		"working pane":    {nil, []string{"--model", "local/m2"}, func(rt *fakeRuntime) { rt.status = "working" }, 3, "is working, not idle at its prompt"},
		"same model":      {nil, []string{"--model", "local/m1"}, nil, 3, "already runs local/m1"},
		"unlisted model":  {nil, []string{"--model", "nope/x"}, nil, 2, "opencode model nope/x is not in `opencode models`"},
		"session unread":  {map[string]string{"getdie": "database is locked"}, []string{"--model", "local/m2"}, nil, 3, "cannot read the model of opencode session"},
		"no model":        {nil, nil, nil, 2, "usage: hand attempt switch"},
		"hash in model":   {nil, []string{"--model", "local/m2#deep"}, nil, 2, "provider/id"},
		"model not slash": {nil, []string{"--model", "m2"}, nil, 2, "provider/id"},
	} {
		fx, o, _ := runningWorker(t, tc.extra)
		if tc.setup != nil {
			fx.rt.set(tc.setup)
		}
		_, errOut, code := fx.h.run(append([]string{"attempt", "switch", "a1"}, tc.args...)...)
		if code != tc.code || !strings.Contains(errOut, tc.want) {
			t.Fatalf("%s: code=%d stderr=%q", name, code, errOut)
		}
		if got := afterStart(o, 2); len(got) != 0 {
			t.Fatalf("%s: events after the start = %q", name, got)
		}
		has(t, name, fx.h.ok("attempt", "show", "a1"), "status: running")
	}
}

func TestAttemptSwitchRefusesASessionOpencodeStillRuns(t *testing.T) {
	list := filepath.Join(t.TempDir(), "sessions")
	fx, o, session := runningWorker(t, map[string]string{"sessionlist": list})
	body := fmt.Sprintf(`{"data":[{"id":%q,"time":{"updated":%d,"idle":0}}]}`, session, time.Now().UnixMilli())
	if err := os.WriteFile(list, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := fx.h.run("attempt", "switch", "a1", "--model", "local/m2")
	if code != 3 || !strings.Contains(errOut, "opencode still runs a turn of attempt a1") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if got := afterStart(o, 2); len(got) != 0 {
		t.Fatalf("events after the start = %q", got)
	}
}

func TestAttemptSwitchRefusesAClaudeAttempt(t *testing.T) {
	fx := newAttemptFixture(t)
	fx.start()
	_, errOut, code := fx.h.run("attempt", "switch", "a1", "--model", "local/m2")
	if code != 3 || !strings.Contains(errOut, "only an opencode attempt") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestAttemptSwitchFailureRelaunchesOnTheOldModel(t *testing.T) {
	fx, o, session := runningWorker(t, map[string]string{"swreject": "Unknown provider nope"})
	_, errOut, code := fx.h.run("attempt", "switch", "a1", "--model", "local/m2")
	if code == 0 || !strings.Contains(errOut, "HTTP 400") || !strings.Contains(errOut, "attempt a1 is back on its pane, still on its old model") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	want := []string{"stop", "switch " + session + ` {"model":{"providerID":"local","id":"m2"}}`, "pane"}
	if got := afterStart(o, 2); !slices.Equal(got, want) {
		t.Fatalf("events after the start = %q, want %q", got, want)
	}
	has(t, "show", fx.h.ok("attempt", "show", "a1"), "status: running")
	if got := attemptSwitchedEvents(t, fx); len(got) != 1 || !strings.HasPrefix(got[0], "a1: back on local/m1, pane ") {
		t.Fatalf("attempt.switched events = %q", got)
	}
}

func TestAttemptSwitchSaysWhenItCannotConfirmTheOldModelAfterAFailedSwitch(t *testing.T) {
	fx, o, _ := runningWorker(t, map[string]string{"swreject": "Unknown provider nope", "getlater": filepath.Join(t.TempDir(), "read")})
	_, errOut, code := fx.h.run("attempt", "switch", "a1", "--model", "local/m2")
	if code == 0 || !strings.Contains(errOut, "attempt a1 is back on its pane, its model is unverified: the watcher records the session's model on its next sweep") || strings.Contains(errOut, "still on its old model") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if got := attemptSwitchedEvents(t, fx); len(got) != 1 || !strings.HasPrefix(got[0], "a1: back on its pane, model unverified, pane ") {
		t.Fatalf("attempt.switched events = %q", got)
	}
	if got := afterStart(o, 2); len(got) != 3 {
		t.Fatalf("events after the start = %q", got)
	}
}

func failPanesAfter(fx *attemptFixture, o *opencodeRig, ok int32) {
	var made atomic.Int32
	fx.rt.srv.Handle("terminal.backend.create", func(p json.RawMessage) (any, error) {
		if made.Add(1) > ok {
			return nil, fakeuhp.Fail{Code: "spawn_failed", Message: "no pty"}
		}
		fakebin.Append(o.log, "pane")
		return fx.rt.create(p)
	})
}

func TestAttemptSwitchWithNoPaneLeftInterruptsTheAttemptWithAWayBack(t *testing.T) {
	fx, o, session := runningWorker(t, map[string]string{"swreject": "Unknown provider nope"})
	failPanesAfter(fx, o, 0)
	_, errOut, code := fx.h.run("attempt", "switch", "a1", "--model", "local/m2")
	if code == 0 || !strings.Contains(errOut, "attempt a1 has no pane and is interrupted") || !strings.Contains(errOut, "opencode session "+session+" stay") || !strings.Contains(errOut, "`hand attempt clean a1`, then `hand attempt start t1 --continue a1`") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	has(t, "show", fx.h.ok("attempt", "show", "a1"), "status: interrupted", "switch to local/m2 failed")
}

func TestAttemptSwitchPutsTheSessionBackWhenThePaneCannotStart(t *testing.T) {
	fx, o, session := runningWorker(t, nil)
	failPanesAfter(fx, o, 0)
	_, errOut, code := fx.h.run("attempt", "switch", "a1", "--model", "local/m2", "--effort", "deep")
	if code == 0 || !strings.Contains(errOut, "attempt a1 has no pane and is interrupted") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	want := []string{"stop", "switch " + session + ` {"model":{"providerID":"local","id":"m2","variant":"deep"}}`, "switch " + session + ` {"model":{"providerID":"local","id":"m1"}}`}
	if got := afterStart(o, 2); !slices.Equal(got, want) {
		t.Fatalf("events after the start = %q, want %q", got, want)
	}
	has(t, "show", fx.h.ok("attempt", "show", "a1"), "status: interrupted")
}

func TestAttemptSwitchSaysWhereTheSessionStaysWhenItsOldModelIsUnknown(t *testing.T) {
	fx, o, session := runningWorker(t, map[string]string{"sessionmodel": ""})
	failPanesAfter(fx, o, 0)
	_, errOut, code := fx.h.run("attempt", "switch", "a1", "--model", "local/m2")
	if code == 0 || !strings.Contains(errOut, "opencode session "+session+" stays on local/m2") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if got := afterStart(o, 2); len(got) != 2 {
		t.Fatalf("events after the start = %q", got)
	}
}

func TestAttemptSwitchSaysWhenTheOldModelCannotBePutBack(t *testing.T) {
	fx, o, session := runningWorker(t, map[string]string{"swdiebody": `"id":"m1"`})
	failPanesAfter(fx, o, 0)
	_, errOut, code := fx.h.run("attempt", "switch", "a1", "--model", "local/m2")
	if code == 0 || !strings.Contains(errOut, "could not put opencode session "+session+" back on local/m1") || !strings.Contains(errOut, "it stays on local/m2") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestAttemptSwitchRefusesAnUnlistedModelEvenForAVariantOnly(t *testing.T) {
	fx, o, _ := runningWorker(t, map[string]string{"models": "local/m2\n"})
	_, errOut, code := fx.h.run("attempt", "switch", "a1", "--model", "local/m1", "--effort", "deep")
	if code != 2 || !strings.Contains(errOut, "opencode model local/m1 is not in `opencode models`") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if got := afterStart(o, 2); len(got) != 0 {
		t.Fatalf("events after the start = %q", got)
	}
}

func TestAttemptSwitchTreatsADonePaneAsAtItsPrompt(t *testing.T) {
	fx, _, _ := runningWorker(t, nil)
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "done" })
	has(t, "switch", fx.h.ok("attempt", "switch", "a1", "--model", "local/m2"), "status: running", "model: local/m2")
}

func TestAttemptSwitchOfAVariantOnlyKeepsTheModelAndRecordsTheVariant(t *testing.T) {
	fx, o, session := runningWorker(t, nil)
	fx.h.ok("attempt", "switch", "a1", "--model", "local/m1", "--effort", "deep")
	want := []string{"stop", "switch " + session + ` {"model":{"providerID":"local","id":"m1","variant":"deep"}}`, "pane"}
	if got := afterStart(o, 2); !slices.Equal(got, want) {
		t.Fatalf("events after the start = %q, want %q", got, want)
	}
	has(t, "show", fx.h.ok("attempt", "show", "a1"), "harness: opencode local/m1 deep")
}

func TestAttemptSwitchRecordsTheNewModelBeforeAnySweep(t *testing.T) {
	fx, _, _ := runningWorker(t, nil)
	fx.h.ok("attempt", "switch", "a1", "--model", "local/m2", "--effort", "deep")
	events, err := openStore(t, fx.h).EventsAfter(context.Background(), 0, []string{"model.changed"}, 10)
	if err != nil || len(events) != 1 || events[0].Detail != "a1: local/m2 deep" {
		t.Fatalf("model.changed events = %+v, %v", events, err)
	}
	has(t, "show", fx.h.ok("attempt", "show", "a1"), "harness: opencode local/m2 deep")
}

func TestAttemptSwitchKeepsTheRecordedModelWhenItComesBackOnTheOldPane(t *testing.T) {
	fx, _, _ := runningWorker(t, map[string]string{"swreject": "Unknown provider nope"})
	fx.h.run("attempt", "switch", "a1", "--model", "local/m2")
	after := fx.h.ok("attempt", "show", "a1")
	has(t, "show", after, "harness: opencode local/m1\n")
	if strings.Contains(after, "local/m2") {
		t.Fatalf("show = %q", after)
	}
}

func TestAttemptSwitchRefusesAStoppedAttempt(t *testing.T) {
	fx, o, _ := runningWorker(t, nil)
	fx.h.ok("attempt", "stop", "a1")
	_, errOut, code := fx.h.run("attempt", "switch", "a1", "--model", "local/m2")
	if code != 3 || !strings.Contains(errOut, "a1") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if got := afterStart(o, 2); len(got) != 1 || got[0] != "stop" {
		t.Fatalf("events after the start = %q", got)
	}
}

func TestAttemptSwitchClosesTheOldTerminalAndKeepsTheNewOne(t *testing.T) {
	fx, _, _ := runningWorker(t, nil)
	fx.h.ok("attempt", "switch", "a1", "--model", "local/m2")
	fx.rt.set(func(rt *fakeRuntime) {
		if len(rt.terms) != 2 {
			t.Fatalf("terminals = %d, want 2", len(rt.terms))
		}
		if old := rt.terms[0]; !old.closed || !exited(old) {
			t.Fatalf("the old terminal is not closed and gone: closed=%v exited=%v", old.closed, exited(old))
		}
		if cur := rt.terms[1]; cur.closed || exited(cur) {
			t.Fatalf("the new terminal was stopped: closed=%v exited=%v", cur.closed, exited(cur))
		}
	})
}

func TestAttemptSwitchRefusesATurnStartedAfterTheFirstCheck(t *testing.T) {
	fx, o, _ := runningWorker(t, nil)
	var asked atomic.Int32
	fx.rt.srv.Handle("agent.explain", func(p json.RawMessage) (any, error) {
		if asked.Add(1) == 2 {
			fx.rt.set(func(rt *fakeRuntime) { rt.status = "working" })
		}
		return fx.rt.explain(p)
	})
	_, errOut, code := fx.h.run("attempt", "switch", "a1", "--model", "local/m2")
	if code != 3 || !strings.Contains(errOut, "is working, not idle at its prompt; nothing changed") || asked.Load() != 2 {
		t.Fatalf("code=%d asked=%d stderr=%q", code, asked.Load(), errOut)
	}
	if got := afterStart(o, 2); len(got) != 0 {
		t.Fatalf("events after the start = %q", got)
	}
	has(t, "show", fx.h.ok("attempt", "show", "a1"), "status: running", "harness: opencode local/m1\n")
}

func TestAttemptSwitchHoldsTheAttemptLockWhileThePaneIsGone(t *testing.T) {
	fx, o, _ := runningWorker(t, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	var once atomic.Bool
	fx.rt.srv.Handle("terminal.backend.create", func(p json.RawMessage) (any, error) {
		if once.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		fakebin.Append(o.log, "pane")
		return fx.rt.create(p)
	})
	type result struct {
		out, errOut string
		code        int
	}
	first := make(chan result, 1)
	go func() {
		out, errOut, code := fx.h.run("attempt", "switch", "a1", "--model", "local/m2")
		first <- result{out, errOut, code}
	}()
	<-entered
	listed := make(chan string, 1)
	go func() { listed <- fx.h.ok("attempt", "list") }()
	second := make(chan result, 1)
	go func() {
		out, errOut, code := fx.h.run("attempt", "switch", "a1", "--model", "local/m2")
		second <- result{out, errOut, code}
	}()
	select {
	case <-listed:
	case <-time.After(30 * time.Second):
		t.Fatal("attempt list did not return while the switch held the lock")
	}
	select {
	case r := <-second:
		t.Fatalf("a second switch ran while the first held the lock: %+v", r)
	case <-time.After(500 * time.Millisecond):
	}
	has(t, "show", fx.h.ok("attempt", "show", "a1"), "status: running")
	close(release)
	if r := <-first; r.code != 0 {
		t.Fatalf("first switch: %+v", r)
	}
	if r := <-second; r.code != 3 || !strings.Contains(r.errOut, "already runs local/m2") {
		t.Fatalf("second switch: %+v", r)
	}
	has(t, "show", fx.h.ok("attempt", "show", "a1"), "status: running")
	if events, err := openStore(t, fx.h).EventsAfter(context.Background(), 0, []string{"attempt.exited", "attempt.interrupted"}, 10); err != nil || len(events) != 0 {
		t.Fatalf("end events = %+v, %v", events, err)
	}
}

func TestAttemptSwitchSaysWhenTheOldTerminalWillNotClose(t *testing.T) {
	fx, _, _ := runningWorker(t, nil)
	fx.rt.set(func(rt *fakeRuntime) { rt.closeFail = "backend_error" })
	out, errOut, code := fx.h.run("attempt", "switch", "a1", "--model", "local/m2")
	if code != 0 || !strings.Contains(out, "its terminal did not close") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
	}
}

func TestAttemptSwitchStoresNoEffortForTheDefaultVariant(t *testing.T) {
	fx, _, _ := runningWorker(t, nil)
	fx.h.ok("attempt", "switch", "a1", "--model", "local/m2", "--effort", "default")
	has(t, "show", fx.h.ok("attempt", "show", "a1"), "harness: opencode local/m2\n")
}

func TestAttemptSwitchRecordsTheModelTheSessionReadsBackAfterAFailedSwitch(t *testing.T) {
	fx, _, _ := runningWorker(t, map[string]string{"swreject": "Unknown provider nope", "sessionmodel": `{"id":"m3","providerID":"local"}`})
	fx.h.run("attempt", "switch", "a1", "--model", "local/m2")
	has(t, "show", fx.h.ok("attempt", "show", "a1"), "harness: opencode local/m3\n")
}

func TestAttemptSwitchRequestsTheNewModelSoTheWatcherShowsNoMismatch(t *testing.T) {
	fx, _, _ := runningWorker(t, nil)
	fx.h.ok("attempt", "switch", "a1", "--model", "local/m2", "--effort", "deep")
	if got, err := openStore(t, fx.h).ModelNote(context.Background(), "a1", "local/m2", "deep"); err != nil || got != "" {
		t.Fatalf("note = %q, %v", got, err)
	}
}

func TestAttemptSwitchThatFailsKeepsTheOldRequest(t *testing.T) {
	fx, _, _ := runningWorker(t, map[string]string{"swreject": "Unknown provider nope"})
	fx.h.run("attempt", "switch", "a1", "--model", "local/m2")
	got, err := openStore(t, fx.h).ModelNote(context.Background(), "a1", "local/m2", "")
	if err != nil || got != "Asked for local/m1, running local/m2." {
		t.Fatalf("note = %q, %v", got, err)
	}
}

func TestAttemptSwitchRecordsTheNewModelWhenTheSwitchErroredAfterApplying(t *testing.T) {
	fx, _, _ := runningWorker(t, map[string]string{"swdie": "reply lost", "swdieapply": "1"})
	_, errOut, code := fx.h.run("attempt", "switch", "a1", "--model", "local/m2")
	if code == 0 || !strings.Contains(errOut, "attempt a1 is back on its pane, its session is on local/m2") || strings.Contains(errOut, "still on its old model") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	has(t, "show", fx.h.ok("attempt", "show", "a1"), "harness: opencode local/m2\n")
	if got := attemptSwitchedEvents(t, fx); len(got) != 1 || !strings.HasPrefix(got[0], "a1: back on its pane, on local/m2, pane ") {
		t.Fatalf("attempt.switched events = %q", got)
	}
}
