package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/state"
)

func runningOpencode(t *testing.T, extra map[string]string, start ...string) (*harness, *fakeRuntime, *opencodeRig, string) {
	t.Helper()
	h, rt := newSupervisorFixture(t)
	wait := *cli.NoteWait
	*cli.NoteWait = time.Second
	t.Cleanup(func() { *cli.NoteWait = wait })
	params := map[string]string{"models": "local/m1\nlocal/m2\n", "msgs": filepath.Join(t.TempDir(), "msgs")}
	for k, v := range extra {
		params[k] = v
	}
	o := installOpencode(t, h, rt, params)
	if extra["lost"] == "" {
		recordPrompts(rt, params["msgs"])
	}
	rt.srv.Handle("terminal.backend.close", func(p json.RawMessage) (any, error) {
		fakebin.Append(o.log, "stop")
		return rt.close(p)
	})
	h.ok(append([]string{"supervisor", "start", "--harness", "opencode"}, start...)...)
	argv := rt.lastCreate().Command
	session := argv[slices.Index(argv, "--session")+1]
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	return h, rt, o, session
}

func recordPrompts(rt *fakeRuntime, msgs string) {
	rt.srv.Handle("agent.prompt", func(p json.RawMessage) (any, error) {
		var body struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(p, &body); err != nil {
			return nil, err
		}
		out, err := rt.prompt(p)
		if err == nil {
			line, _ := json.Marshal(body.Text)
			fakebin.Append(msgs, string(line))
		}
		return out, err
	})
}

func afterStart(o *opencodeRig, n int) []string {
	ev := o.events()
	if len(ev) < n {
		return nil
	}
	return ev[n:]
}

func TestOpencodeSwitchStopsSwitchesTheModelAndResumesTheSameSession(t *testing.T) {
	h, rt, o, session := runningOpencode(t, nil, "--model", "local/m1")
	out := h.ok("supervisor", "switch", "--harness", "opencode", "--model", "local/m2", "--effort", "deep")
	has(t, "switch", out, "supervisor: s2", "switch: applied", "model: local/m2", "effort: deep")
	want := []string{"stop", "switch " + session + ` {"model":{"providerID":"local","id":"m2","variant":"deep"}}`, "pane"}
	if got := afterStart(o, 2); !slices.Equal(got, want) {
		t.Fatalf("events after the start = %q, want %q", got, want)
	}
	argv := rt.lastCreate().Command
	if !slices.Equal(argv[1:], []string{"--standalone", "--auto", "--session", session}) {
		t.Fatalf("relaunch argv = %q", argv)
	}
	has(t, "show", h.ok("supervisor", "show"), "supervisor: s2", "resumes: s1", "harness: opencode local/m2 deep", "session: "+session)
}

func TestOpencodeSwitchWithoutAVariantSendsNone(t *testing.T) {
	h, _, o, session := runningOpencode(t, nil, "--model", "local/m1")
	has(t, "switch", h.ok("supervisor", "switch", "--model", "local/m2"), "switch: applied", "model: local/m2")
	if got := afterStart(o, 2); len(got) != 3 || got[1] != "switch "+session+` {"model":{"providerID":"local","id":"m2"}}` {
		t.Fatalf("events after the start = %q", got)
	}
}

func TestOpencodeSwitchOfTheVariantAloneKeepsTheModel(t *testing.T) {
	h, _, o, session := runningOpencode(t, nil, "--model", "local/m2", "--effort", "fast")
	has(t, "switch", h.ok("supervisor", "switch", "--effort", "deep"), "switch: applied", "model: local/m2", "effort: deep")
	if got := afterStart(o, 2); len(got) != 3 || got[1] != "switch "+session+` {"model":{"providerID":"local","id":"m2","variant":"deep"}}` {
		t.Fatalf("events after the start = %q", got)
	}
}

func TestOpencodeSwitchWhileWorkingCallsNothing(t *testing.T) {
	h, rt, o, _ := runningOpencode(t, nil, "--model", "local/m1")
	rt.set(func(rt *fakeRuntime) { rt.status = "working" })
	out := h.ok("supervisor", "switch", "--model", "local/m2", "--effort", "deep")
	has(t, "switch", out, "supervisor: s1", "switch: pending", "why: switching to local/m2 deep after this turn")
	if got := afterStart(o, 2); len(got) != 0 || len(o.events()) != 2 {
		t.Fatalf("events = %q", o.events())
	}
}

func TestOpencodeSwitchRefusesWhatCannotWork(t *testing.T) {
	h, _, o, _ := runningOpencode(t, nil)
	for _, c := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"--effort", "deep"}, 2, "variant of a model"},
		{[]string{"--model", "local/m2#deep"}, 2, "provider/id"},
		{[]string{"--model", "m2"}, 2, "provider/id"},
	} {
		_, errOut, code := h.run(append([]string{"supervisor", "switch"}, c.args...)...)
		if code != c.code || !strings.Contains(errOut, c.want) {
			t.Fatalf("switch %q: code=%d stderr=%q", c.args, code, errOut)
		}
	}
	if len(o.events()) != 1 {
		t.Fatalf("events = %q", o.events())
	}
}

func TestOpencodeSwitchFailureLeavesTheSupervisorStoppedWithTheResumeHint(t *testing.T) {
	for name, tc := range map[string]struct {
		extra map[string]string
		want  string
	}{
		"HTTP 400":        {map[string]string{"swreject": "Unknown provider nope"}, "HTTP 400"},
		"exit code":       {map[string]string{"swdie": "database is locked"}, "database is locked"},
		"mismatched body": {map[string]string{"swanswer": `{"data":{"id":"ses_other"}}`}, "not an empty reply"},
	} {
		h, _, o, session := runningOpencode(t, tc.extra, "--model", "local/m1")
		_, errOut, code := h.run("supervisor", "switch", "--model", "local/m2", "--effort", "deep")
		if code == 0 || !strings.Contains(errOut, tc.want) || !strings.Contains(errOut, "s1 stopped for the switch to local/m2 deep, continue it with `hand supervisor resume`, or drop the switch to local/m2 deep: `hand supervisor switch --cancel`") {
			t.Fatalf("%s: code=%d stderr=%q", name, code, errOut)
		}
		if strings.Contains(errOut, " : ") || !strings.Contains(errOut, "switching opencode session "+session) {
			t.Fatalf("%s: stderr=%q", name, errOut)
		}
		if got := afterStart(o, 2); len(got) != 2 || got[0] != "stop" || !strings.HasPrefix(got[1], "switch "+session) {
			t.Fatalf("%s: events after the start = %q", name, got)
		}
		has(t, name, h.ok("supervisor", "show"), "supervisor: s1", "status: stopped")
	}
}

func TestOpencodeResumeAfterAPendingSwitchSwitchesBeforeTheNewPane(t *testing.T) {
	h, rt, o, session := runningOpencode(t, nil, "--model", "local/m1")
	rt.set(func(rt *fakeRuntime) { rt.status = "working" })
	has(t, "switch", h.ok("supervisor", "switch", "--model", "local/m2", "--effort", "deep"), "switch: pending")
	h.ok("supervisor", "stop")
	has(t, "resume", h.ok("supervisor", "resume"), "supervisor: s2")
	want := []string{"stop", "switch " + session + ` {"model":{"providerID":"local","id":"m2","variant":"deep"}}`, "pane"}
	if got := afterStart(o, 2); !slices.Equal(got, want) {
		t.Fatalf("events after the start = %q, want %q", got, want)
	}
}

func TestSwitchingFromClaudeToOpencodeStartsAFreshSession(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	o := installOpencode(t, h, rt, map[string]string{"models": "local/m1\nlocal/m2\n"})
	startClaudeSupervisor(h)
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	has(t, "switch", h.ok("supervisor", "switch", "--harness", "opencode", "--model", "local/m2", "--effort", "deep"), "supervisor: s2", "harness: opencode")
	ev := o.events()
	if len(ev) != 3 || ev[0] != "pane" || !strings.HasPrefix(ev[1], "create ") || ev[2] != "pane" {
		t.Fatalf("events = %q, want the claude pane, then session.create, then the opencode pane", ev)
	}
	var body createBody
	if err := json.Unmarshal([]byte(strings.TrimPrefix(ev[1], "create ")), &body); err != nil {
		t.Fatal(err)
	}
	if body.Model.ProviderID != "local" || body.Model.ID != "m2" || body.Model.Variant == nil || *body.Model.Variant != "deep" {
		t.Fatalf("body = %+v", body)
	}
}

func TestOpencodeSwitchToAnotherModelSendsNoVariantAndDefaultIsNotStored(t *testing.T) {
	h, _, o, session := runningOpencode(t, nil, "--model", "local/m2", "--effort", "fast")
	out := h.ok("supervisor", "switch", "--model", "local/m1")
	has(t, "switch", out, "switch: applied", "model: local/m1")
	if strings.Contains(out, "effort:") {
		t.Fatalf("switch = %q", out)
	}
	if got := afterStart(o, 2); len(got) != 3 || got[1] != "switch "+session+` {"model":{"providerID":"local","id":"m1"}}` {
		t.Fatalf("events after the start = %q", got)
	}
	has(t, "show", h.ok("supervisor", "show"), "harness: opencode local/m1\n")
	has(t, "default", h.ok("supervisor", "switch", "--model", "local/m2", "--effort", "default"), "switch: applied", "model: local/m2")
	if got := afterStart(o, 5); len(got) != 3 || got[1] != "switch "+session+` {"model":{"providerID":"local","id":"m2","variant":"default"}}` {
		t.Fatalf("events after the second switch = %q", got)
	}
	has(t, "show", h.ok("supervisor", "show"), "harness: opencode local/m2\n")
	if _, errOut, code := h.run("supervisor", "switch", "--effort", "default"); code != 3 || !strings.HasSuffix(strings.TrimSpace(errOut), "already runs local/m2") {
		t.Fatalf("default on a model with no variant: code=%d stderr=%q", code, errOut)
	}
}

func TestOpencodeSwitchRefusesAModelOpencodeDoesNotList(t *testing.T) {
	h, _, o, _ := runningOpencode(t, nil, "--model", "local/m1")
	_, errOut, code := h.run("supervisor", "switch", "--model", "nope/x")
	if code != 2 || !strings.Contains(errOut, "opencode model nope/x is not in `opencode models`") || strings.Contains(errOut, "starting anyway") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if len(o.events()) != 2 {
		t.Fatalf("events = %q", o.events())
	}
	has(t, "show", h.ok("supervisor", "show"), "supervisor: s1", "status: running")
}

func TestOpencodeSwitchRefreshesTheModelListOnceBeforeRefusing(t *testing.T) {
	more := filepath.Join(t.TempDir(), "more")
	h, _, o, session := runningOpencode(t, map[string]string{"more": more}, "--model", "local/m1")
	if err := os.WriteFile(more, []byte("late/m9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := h.ok("supervisor", "switch", "--model", "late/m9")
	has(t, "switch", out, "switch: applied", "model: late/m9")
	if strings.Contains(out, "warn:") {
		t.Fatalf("switch = %q", out)
	}
	if got := afterStart(o, 2); len(got) != 3 || got[1] != "switch "+session+` {"model":{"providerID":"late","id":"m9"}}` {
		t.Fatalf("events after the start = %q", got)
	}
}

const lostNote = "opencode did not take the resume note: is local/m2 available? Switch back with `hand supervisor switch --model local/m1`"

func blockedNoted(t *testing.T, h *harness, detail string) bool {
	events, err := openStore(t, h).EventsAfter(context.Background(), 0, []string{"supervisor.blocked"}, 10)
	return err == nil && slices.ContainsFunc(events, func(e state.Event) bool { return e.Detail == detail })
}

func lostNoteNoted(t *testing.T, h *harness) bool {
	return blockedNoted(t, h, "s2: "+lostNote)
}

func pendingInputs(t *testing.T, h *harness) []state.SupervisorInput {
	t.Helper()
	pending, err := openStore(t, h).PendingSupervisorInputs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return pending
}

func TestOpencodeSwitchSaysWhenTheNewModelDoesNotTakeTheResumeNote(t *testing.T) {
	h, _, _, _ := runningOpencode(t, map[string]string{"lost": "1"}, "--model", "local/m1")
	out := h.ok("supervisor", "switch", "--model", "local/m2", "--effort", "deep")
	has(t, "switch", out, "supervisor: s2", "switch: \"applied, but "+lostNote+"\"")
	if strings.Contains(out, "switch: applied\n") || strings.Contains(out, "warn:") {
		t.Fatalf("switch = %q", out)
	}
	if !lostNoteNoted(t, h) {
		t.Fatalf("no blocked event %q", lostNote)
	}
}

func notifications(t *testing.T, path string) int {
	b, _ := os.ReadFile(path)
	return strings.Count(string(b), "did not take the resume note")
}

func TestAWatcherThatAppliesASwitchTheNewModelDoesNotAnswerKeepsTheInputsPending(t *testing.T) {
	msgs := filepath.Join(t.TempDir(), "msgs")
	h, rt, _, _ := runningOpencode(t, map[string]string{"lost": "1", "msgs": msgs}, "--model", "local/m1")
	fx := &attemptFixture{h: h, rt: rt}
	notes := fakeNotify(t, fx)
	rt.set(func(rt *fakeRuntime) { rt.status = "working" })
	has(t, "switch", h.ok("supervisor", "switch", "--model", "local/m2"), "switch: pending")
	has(t, "send", h.ok("supervisor", "send", "--text", "operator message"), "delivered: no")
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	stop := startWatch(t, fx, "--every", "100ms")
	eventually(t, func() bool { return lostNoteNoted(t, h) })
	time.Sleep(800 * time.Millisecond)
	out, _ := stop()
	if n := strings.Count(out, "observed: \""+lostNote+"\""); n != 1 {
		t.Fatalf("observed %d times, want 1: %q", n, out)
	}
	if runtime.GOOS != "windows" {
		if n := notifications(t, notes); n != 1 {
			t.Fatalf("notified %d times, want 1", n)
		}
	}
	has(t, "later send", h.ok("supervisor", "send", "--text", "again"), "delivered: no", "opencode did not take the resume note")
	if got := rt.prompts(); !slices.Equal(got, []string{"[hand v1 resume] you are now s2 (resumes s1)"}) {
		t.Fatalf("prompts = %q", got)
	}
	if pending := pendingInputs(t, h); len(pending) != 2 || pending[0].Body != "operator message" {
		t.Fatalf("pending = %+v", pending)
	}
	recordPrompts(rt, msgs)
	h.ok("supervisor", "switch", "--model", "local/m1")
	if pending := pendingInputs(t, h); len(pending) != 0 {
		t.Fatalf("pending after the switch back = %+v", pending)
	}
	if got := rt.prompts(); !slices.Equal(got[len(got)-2:], []string{"operator message", "again"}) {
		t.Fatalf("prompts = %q", got)
	}
}

const lostResume = "opencode did not take the resume note: is local/m2 available? Switch to another model with `hand supervisor switch --model M`"

func TestResumingAHeldSupervisorOnTheSameModelHoldsTheMessagesAgain(t *testing.T) {
	h, rt, _, _ := runningOpencode(t, map[string]string{"lost": "1"}, "--model", "local/m1")
	h.ok("supervisor", "switch", "--model", "local/m2")
	has(t, "send", h.ok("supervisor", "send", "--text", "operator message"), "delivered: no")
	has(t, "show", h.ok("supervisor", "show"), "held: \""+lostNote+"\"")
	h.ok("supervisor", "stop")
	out := h.ok("supervisor", "resume")
	has(t, "resume", out, "supervisor: s3", lostResume)
	if strings.Contains(out, "Switch back") {
		t.Fatalf("resume = %q", out)
	}
	if !blockedNoted(t, h, "s3: "+lostResume) {
		t.Fatal("no blocked event for s3")
	}
	has(t, "send", h.ok("supervisor", "send", "--text", "again"), "delivered: no", "opencode did not take the resume note")
	if pending := pendingInputs(t, h); len(pending) != 2 {
		t.Fatalf("pending = %+v", pending)
	}
	if got := rt.prompts(); len(got) != 2 || strings.Contains(strings.Join(got, ""), "message") {
		t.Fatalf("prompts = %q", got)
	}
	has(t, "orient", h.ok("orient"), "holds messages")
	has(t, "show", h.ok("supervisor", "show"), "held:", "s3")
}

func TestAnAutoresumeOnTheModelThatDidNotAnswerHoldsTheMessagesAgain(t *testing.T) {
	h, rt, _, _ := runningOpencode(t, map[string]string{"lost": "1"}, "--model", "local/m1")
	policy := `{"profiles":{"default":{"harness":"claude","model":"sonnet","effort":"medium"}},"supervisor":{"autoresume":true}}`
	if err := os.WriteFile(filepath.Join(h.home, "routing.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	h.ok("supervisor", "switch", "--model", "local/m2")
	h.ok("supervisor", "send", "--text", "operator message")
	rt.srv.SetGeneration("gen-2")
	stop := startWatch(t, &attemptFixture{h: h, rt: rt}, "--every", "1h")
	eventually(t, func() bool { return blockedNoted(t, h, "s3: "+lostResume) })
	stop()
	if pending := pendingInputs(t, h); len(pending) != 1 {
		t.Fatalf("pending = %+v", pending)
	}
	if got := rt.prompts(); len(got) != 2 || strings.Contains(strings.Join(got, ""), "message") {
		t.Fatalf("prompts = %q", got)
	}
}

func TestOpencodeSwitchFromTheDefaultModelDoesNotNameAModelToSwitchBackTo(t *testing.T) {
	h, _, _, _ := runningOpencode(t, map[string]string{"lost": "1"})
	out := h.ok("supervisor", "switch", "--model", "local/m2")
	has(t, "switch", out, "Switch to another model with `hand supervisor switch --model M`")
	if strings.Contains(out, "--model `") || strings.Contains(out, "--model \"") || strings.Contains(out, "Switch back") {
		t.Fatalf("switch = %q", out)
	}
}

func TestACheckThatCannotReadTheSessionHoldsNothing(t *testing.T) {
	h, _, _, _ := runningOpencode(t, map[string]string{"listdie": "database is locked"}, "--model", "local/m1")
	out := h.ok("supervisor", "switch", "--model", "local/m2")
	has(t, "switch", out, "switch: applied", "could not check that opencode took the resume note")
	if blocked, ok, err := openStore(t, h).LatestEvent(context.Background(), "supervisor.blocked"); err != nil || ok && strings.Contains(blocked.Detail, state.NoteLost) {
		t.Fatalf("blocked = %+v, %v", blocked, err)
	}
	has(t, "send", h.ok("supervisor", "send", "--text", "operator message"), "delivered: yes")
}

func TestOpencodeSwitchFailureCanBeDroppedAndTheSupervisorResumesOnItsOldModel(t *testing.T) {
	h, rt, o, session := runningOpencode(t, map[string]string{"swdie": "database is locked"}, "--model", "local/m1", "--effort", "fast")
	if _, _, code := h.run("supervisor", "switch", "--model", "local/m2", "--effort", "deep"); code == 0 {
		t.Fatal("the switch did not fail")
	}
	_, errOut, code := h.run("supervisor", "resume")
	if code == 0 || !strings.Contains(errOut, "; retry with `hand supervisor resume`, or drop the switch to local/m2 deep: `hand supervisor switch --cancel`") {
		t.Fatalf("resume code=%d stderr=%q", code, errOut)
	}
	has(t, "cancel", h.ok("supervisor", "switch", "--cancel"), "supervisor: s1", "switch: canceled")
	if _, _, code := h.run("supervisor", "switch", "--cancel"); code != 3 {
		t.Fatalf("second cancel code = %d", code)
	}
	before := len(o.events())
	has(t, "resume", h.ok("supervisor", "resume"), "supervisor: s2", "session: "+session)
	if got := o.events()[before:]; !slices.Equal(got, []string{"pane"}) {
		t.Fatalf("events after the resume = %q", got)
	}
	argv := rt.lastCreate().Command
	if !slices.Equal(argv[1:], []string{"--standalone", "--auto", "--session", session}) {
		t.Fatalf("resume argv = %q", argv)
	}
	has(t, "show", h.ok("supervisor", "show"), "harness: opencode local/m1 fast")
}

func TestOpencodeResumeRetriesAFailedSwitchAndSucceeds(t *testing.T) {
	once := filepath.Join(t.TempDir(), "once")
	h, _, o, session := runningOpencode(t, map[string]string{"swonce": once}, "--model", "local/m1")
	if _, errOut, code := h.run("supervisor", "switch", "--model", "local/m2", "--effort", "deep"); code == 0 || !strings.Contains(errOut, "database is locked") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	has(t, "resume", h.ok("supervisor", "resume"), "supervisor: s2")
	body := `{"model":{"providerID":"local","id":"m2","variant":"deep"}}`
	want := []string{"stop", "switch " + session + " " + body, "switch " + session + " " + body, "pane"}
	if got := afterStart(o, 2); !slices.Equal(got, want) {
		t.Fatalf("events after the start = %q, want %q", got, want)
	}
	has(t, "show", h.ok("supervisor", "show"), "harness: opencode local/m2 deep")
}

func TestTheBoardSaysWhereToSwitchBackFrom(t *testing.T) {
	h, _, _, _ := runningOpencode(t, map[string]string{"lost": "1"}, "--model", "local/m1", "--effort", "fast")
	h.board = true
	out := h.ok("supervisor", "switch", "--model", "local/m2")
	has(t, "switch", out, "Switch back to local/m1 fast from the model menu")
	if strings.Contains(out, "hand supervisor switch") {
		t.Fatalf("switch = %q", out)
	}
}
