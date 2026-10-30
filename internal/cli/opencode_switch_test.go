package cli_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fakebin"
)

func runningOpencode(t *testing.T, extra map[string]string, start ...string) (*harness, *fakeRuntime, *opencodeRig, string) {
	t.Helper()
	h, rt := newSupervisorFixture(t)
	params := map[string]string{"models": "local/m1\nlocal/m2\n"}
	for k, v := range extra {
		params[k] = v
	}
	o := installOpencode(t, h, rt, params)
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
		if code == 0 || !strings.Contains(errOut, tc.want) || !strings.Contains(errOut, "s1 stopped for the switch to local/m2 deep, continue it with `hand supervisor resume`") {
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
