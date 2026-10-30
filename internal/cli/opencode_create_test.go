package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fakebin"
)

type opencodeRig struct {
	log string
	rt  *fakeRuntime
	h   *harness
}

func installOpencode(t *testing.T, h *harness, rt *fakeRuntime, extra map[string]string) *opencodeRig {
	t.Helper()
	log := filepath.Join(t.TempDir(), "order.log")
	params := map[string]string{"log": log, "models": "local/m2\nopenrouter/meta/llama:free@v2 beta\n"}
	for k, v := range extra {
		params[k] = v
	}
	fakebin.Install(t, h.vars["PATH"], "opencode", "ocapi", params)
	rt.srv.Handle("terminal.backend.create", func(p json.RawMessage) (any, error) {
		fakebin.Append(log, "pane")
		return rt.create(p)
	})
	rt.set(func(rt *fakeRuntime) {
		rt.screen, rt.revision, rt.status, rt.afterKeys = opencodeScreen+"\n┃  You are supervisor s1 of the Hand fleet\n┃  You are supervisor s3 of the Hand fleet", 7, "idle", "working"
	})
	h.ok("models", "--refresh", "opencode")
	return &opencodeRig{log, rt, h}
}

func (o *opencodeRig) events() []string {
	b, err := os.ReadFile(o.log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n"), "\n")
}

type createBody struct {
	ID    string `json:"id"`
	Model struct {
		ProviderID string `json:"providerID"`
		ID         string `json:"id"`
		Variant    *string
	} `json:"model"`
	Location struct {
		Directory string `json:"directory"`
	} `json:"location"`
}

func (o *opencodeRig) created(t *testing.T) (createBody, string) {
	t.Helper()
	ev := o.events()
	if len(ev) != 2 || !strings.HasPrefix(ev[0], "create ") || ev[1] != "pane" {
		t.Fatalf("events = %q, want session.create and then the pane", ev)
	}
	raw := strings.TrimPrefix(ev[0], "create ")
	var body createBody
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("body %q: %v", raw, err)
	}
	argv := o.rt.lastCreate().Command
	i := slices.Index(argv, "--session")
	if i < 0 || argv[i+1] != body.ID || !hasOpencodeID(body.ID) {
		t.Fatalf("argv = %q, body id = %q", argv, body.ID)
	}
	return body, raw
}

func TestOpencodeWorkerSessionIsCreatedOnItsModelBeforeThePane(t *testing.T) {
	fx := newAttemptFixture(t)
	o := installOpencode(t, fx.h, fx.rt, nil)
	out := fx.h.ok("attempt", "start", "--harness", "opencode", "--model", "local/m2", "--effort", "deep", "--prompt-file", fx.brief, "t1")
	if strings.Contains(out, "warn") {
		t.Fatalf("start = %q", out)
	}
	body, _ := o.created(t)
	if body.Model.ProviderID != "local" || body.Model.ID != "m2" || body.Model.Variant == nil || *body.Model.Variant != "deep" || body.Location.Directory != fx.h.worktree("t1-a1") {
		t.Fatalf("body = %+v", body)
	}
	if list := fx.h.ok("attempt", "list"); !strings.Contains(list, "a1,t1,opencode,local/m2,running") {
		t.Fatalf("list = %q", list)
	}
}

func TestOpencodeWorkerModelKeepsEverythingAfterTheFirstSlash(t *testing.T) {
	fx := newAttemptFixture(t)
	o := installOpencode(t, fx.h, fx.rt, nil)
	out := fx.h.ok("attempt", "start", "--harness", "opencode", "--model", "openrouter/meta/llama:free@v2 beta", "--prompt-file", fx.brief, "t1")
	if strings.Contains(out, "warn") {
		t.Fatalf("start = %q", out)
	}
	body, _ := o.created(t)
	if body.Model.ProviderID != "openrouter" || body.Model.ID != "meta/llama:free@v2 beta" {
		t.Fatalf("body = %+v", body)
	}
}

func TestOpencodeCreateOmitsAnEmptyVariant(t *testing.T) {
	fx := newAttemptFixture(t)
	o := installOpencode(t, fx.h, fx.rt, nil)
	fx.h.ok("attempt", "start", "--harness", "opencode", "--model", "local/m2", "--prompt-file", fx.brief, "t1")
	body, raw := o.created(t)
	if body.Model.ID != "m2" || strings.Contains(raw, "variant") {
		t.Fatalf("body = %s", raw)
	}
}

func TestOpencodeWithoutAModelCreatesNoSession(t *testing.T) {
	fx := newAttemptFixture(t)
	o := installOpencode(t, fx.h, fx.rt, nil)
	fx.h.ok("attempt", "start", "--harness", "opencode", "--prompt-file", fx.brief, "t1")
	if ev := o.events(); !slices.Equal(ev, []string{"pane"}) {
		t.Fatalf("events = %q", ev)
	}
}

func TestOpencodeUnlistedModelWarnsAndStarts(t *testing.T) {
	fx := newAttemptFixture(t)
	o := installOpencode(t, fx.h, fx.rt, nil)
	out := fx.h.ok("attempt", "start", "--harness", "opencode", "--model", "local/m9", "--prompt-file", fx.brief, "t1")
	if !strings.Contains(out, "opencode model local/m9 is not in `opencode models`") {
		t.Fatalf("start = %q", out)
	}
	o.created(t)
}

func TestOpencodeWorkerRefusesAHashInTheModelBeforeTouchingGit(t *testing.T) {
	fx := newAttemptFixture(t)
	o := installOpencode(t, fx.h, fx.rt, nil)
	_, errOut, code := fx.h.run("attempt", "start", "--harness", "opencode", "--model", "local/m2#deep", "--prompt-file", fx.brief, "t1")
	if code != 2 || !strings.Contains(errOut, "provider/id") || len(o.events()) != 0 {
		t.Fatalf("code=%d stderr=%q events=%q", code, errOut, o.events())
	}
}

func TestOpencodeWorkerLaunchFailsWithOpencodesMessageAndNoPane(t *testing.T) {
	for name, extra := range map[string]map[string]string{
		"HTTP 400":  {"reject": "Expected a string starting with ses"},
		"exit code": {"die": "database is locked"},
	} {
		fx := newAttemptFixture(t)
		o := installOpencode(t, fx.h, fx.rt, extra)
		_, errOut, code := fx.h.run("attempt", "start", "--harness", "opencode", "--model", "local/m2", "--effort", "deep", "--prompt-file", fx.brief, "t1")
		want := extra["reject"] + extra["die"]
		if code == 0 || !strings.Contains(errOut, want) {
			t.Fatalf("%s: code=%d stderr=%q", name, code, errOut)
		}
		if strings.Contains(errOut, " : ") {
			t.Fatalf("%s: stray separator in %q", name, errOut)
		}
		if name == "HTTP 400" && !strings.Contains(errOut, "HTTP 400") {
			t.Fatalf("%s: stderr=%q", name, errOut)
		}
		if ev := o.events(); len(ev) != 1 || !strings.HasPrefix(ev[0], "create ") {
			t.Fatalf("%s: events = %q", name, ev)
		}
		if show := fx.h.ok("attempt", "show", "a1"); !strings.Contains(show, "status: failed") || !strings.Contains(show, want) {
			t.Fatalf("%s: show = %q", name, show)
		}
		if _, err := os.Stat(fx.h.worktree("t1-a1")); err == nil {
			t.Fatalf("%s: worktree left behind", name)
		}
	}
}

func TestOpencodeWorkerLaunchFailsWhenTheAnswerIsNotTheRequestedSession(t *testing.T) {
	for name, tc := range map[string]struct {
		extra map[string]string
		want  string
	}{
		"another id":  {map[string]string{"answer": "ses_other"}, `answered with session "ses_other"`},
		"garbage":     {map[string]string{"garbage": "not json"}, "answer is not a session"},
		"no data id":  {map[string]string{"garbage": `{"data":{}}`}, `answered with session ""`},
		"empty reply": {map[string]string{"garbage": " "}, "answer is not a session"},
	} {
		fx := newAttemptFixture(t)
		o := installOpencode(t, fx.h, fx.rt, tc.extra)
		_, errOut, code := fx.h.run("attempt", "start", "--harness", "opencode", "--model", "local/m2", "--prompt-file", fx.brief, "t1")
		if code == 0 || !strings.Contains(errOut, tc.want) {
			t.Fatalf("%s: code=%d stderr=%q", name, code, errOut)
		}
		if ev := o.events(); len(ev) != 1 || !strings.HasPrefix(ev[0], "create ") {
			t.Fatalf("%s: events = %q", name, ev)
		}
		if show := fx.h.ok("attempt", "show", "a1"); !strings.Contains(show, "status: failed") || !strings.Contains(show, "creating opencode session") {
			t.Fatalf("%s: show = %q", name, show)
		}
	}
}

func TestOpencodeSupervisorLaunchFailsWhenTheAnswerIsAnotherSession(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	o := installOpencode(t, h, rt, map[string]string{"answer": "ses_other"})
	_, errOut, code := h.run("supervisor", "start", "--harness", "opencode", "--model", "local/m2")
	if code == 0 || !strings.Contains(errOut, `answered with session "ses_other"`) {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if ev := o.events(); len(ev) != 1 || !strings.HasPrefix(ev[0], "create ") {
		t.Fatalf("events = %q", ev)
	}
	has(t, "show", h.ok("supervisor", "show"), "status: failed", "answered with session", "ses_other")
}

func TestOpencodeSupervisorSessionIsCreatedOnItsModelBeforeThePane(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	o := installOpencode(t, h, rt, nil)
	out := h.ok("supervisor", "start", "--harness", "opencode", "--model", "local/m2", "--effort", "deep")
	has(t, "start", out, "supervisor: s1")
	body, _ := o.created(t)
	if body.Model.ProviderID != "local" || body.Model.ID != "m2" || body.Model.Variant == nil || *body.Model.Variant != "deep" || body.Location.Directory != h.home {
		t.Fatalf("body = %+v", body)
	}
	has(t, "show", h.ok("supervisor", "show"), "harness: opencode local/m2 deep", "session: "+body.ID)
}

func TestOpencodeSupervisorLaunchFailsWithOpencodesMessageAndNoPane(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	o := installOpencode(t, h, rt, map[string]string{"reject": "Unknown provider nope"})
	_, errOut, code := h.run("supervisor", "start", "--harness", "opencode", "--model", "local/m2")
	if code == 0 || !strings.Contains(errOut, "Unknown provider nope") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if ev := o.events(); len(ev) != 1 || !strings.HasPrefix(ev[0], "create ") {
		t.Fatalf("events = %q", ev)
	}
	has(t, "show", h.ok("supervisor", "show"), "status: failed", "Unknown provider nope")
}

func TestOpencodeSupervisorStartAndResumeKeepTheModelAndResumeCreatesNoSession(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	o := installOpencode(t, h, rt, nil)
	h.ok("supervisor", "start", "--harness", "opencode", "--model", "local/m2", "--effort", "deep")
	first, _ := o.created(t)
	h.ok("supervisor", "stop")
	resumed := h.ok("supervisor", "resume")
	has(t, "resume", resumed, "supervisor: s2", "session: "+first.ID)
	if ev := o.events(); !slices.Equal(ev[2:], []string{"pane"}) || len(ev) != 3 {
		t.Fatalf("events after resume = %q", ev)
	}
	argv := rt.lastCreate().Command
	if !slices.Equal(argv[1:], []string{"--standalone", "--auto", "--session", first.ID}) {
		t.Fatalf("resume argv = %q", argv)
	}
	has(t, "show", h.ok("supervisor", "show"), "harness: opencode local/m2 deep")
	h.ok("supervisor", "stop")
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	h.ok("supervisor", "start")
	ev := o.events()
	if len(ev) != 5 || !strings.HasPrefix(ev[3], "create ") || ev[4] != "pane" || !strings.Contains(ev[3], `"variant":"deep"`) || !strings.Contains(ev[3], `"id":"m2"`) || strings.Contains(ev[3], first.ID) {
		t.Fatalf("events after a start with no flags = %q", ev)
	}
}
