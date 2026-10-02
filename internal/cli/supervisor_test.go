package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/agydb/agytest"
	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/flock"
	hh "github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
	"github.com/atqamz/hand/internal/state"
)

var launchPrompt = regexp.MustCompile("^You are supervisor (s[0-9]+) of the Hand fleet \\S+\\. Follow AGENTS\\.md: run `hand orient` now, then work from the operator's messages and from messages that start with \\[hand v1 wake\\]\\. `hand` is `(/[^`]+)`: when `hand` is not on your PATH, run that path, and never run another `hand`\\.$")

func newSupervisorFixture(t *testing.T) (*harness, *fakeRuntime) {
	t.Helper()
	h := newHarness(t)
	rt := startRuntime(t, filepath.Join(t.TempDir(), "uhp.sock"))
	h.vars["HAND_LUVUS_SOCKET"] = rt.srv.Socket
	h.vars["PATH"] = fakeBin(t)
	h.vars["HOME"] = t.TempDir()
	h.ok("init")
	return h, rt
}

func has(t *testing.T, what, out string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Fatalf("%s = %q, missing %q", what, out, want)
		}
	}
}

func startClaudeSupervisor(h *harness) string {
	h.t.Helper()
	return h.ok("supervisor", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low")
}

func TestSupervisorStartLaunchesABackgroundTerminal(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	out := startClaudeSupervisor(h)
	call := rt.lastCreate()
	if call.Label != "hand-supervisor" || call.CWD != h.home {
		t.Fatalf("create = %+v", call)
	}
	argv := call.Command
	if len(argv) != 9 || !strings.HasSuffix(argv[0], "/claude") || !slices.Equal(argv[1:3], []string{"--dangerously-skip-permissions", "--session-id"}) || !slices.Equal(argv[4:8], []string{"--model", "sonnet", "--effort", "low"}) {
		t.Fatalf("argv = %q", argv)
	}
	if m := launchPrompt.FindStringSubmatch(argv[8]); m == nil || m[1] != "s1" {
		t.Fatalf("prompt = %q", argv[8])
	} else if exe, err := os.Executable(); err != nil || m[2] != exe {
		t.Fatalf("prompt names Hand as %q, want %q (%v)", m[2], exe, err)
	}
	has(t, "start", out, "supervisor: s1", "harness: claude", "status: running", "pane: 2", "session: "+argv[3])
	has(t, "show", h.ok("supervisor", "show"), "supervisor: s1", "status: running", "session: "+argv[3], "agent: working", "pending: 0")
	if _, errOut, code := h.run("supervisor", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low"); code != 3 || !strings.Contains(errOut, "already live") {
		t.Fatalf("second start code=%d stderr=%q", code, errOut)
	}
}

func TestSupervisorStartReusesTheLastSettings(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	if _, errOut, code := h.run("supervisor", "start"); code != 2 || !strings.Contains(errOut, "--harness") {
		t.Fatalf("first bare start code=%d stderr=%q", code, errOut)
	}
	startClaudeSupervisor(h)
	first := rt.lastCreate().Command[3]
	has(t, "stop", h.ok("supervisor", "stop"), "supervisor: s1", "status: stopped")
	has(t, "restart", h.ok("supervisor", "start"), "supervisor: s2", "status: running")
	argv := rt.lastCreate().Command
	if argv[2] != "--session-id" || argv[3] == first || !slices.Equal(argv[4:8], []string{"--model", "sonnet", "--effort", "low"}) {
		t.Fatalf("argv = %q, first session %s", argv, first)
	}
	if m := launchPrompt.FindStringSubmatch(argv[8]); m == nil || m[1] != "s2" {
		t.Fatalf("prompt = %q", argv[8])
	}
}

func TestSupervisorResumeContinuesTheSession(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	if _, errOut, code := h.run("supervisor", "resume"); code != 3 || !strings.Contains(errOut, "supervisor start") {
		t.Fatalf("resume with none code=%d stderr=%q", code, errOut)
	}
	startClaudeSupervisor(h)
	session := rt.lastCreate().Command[3]
	cursor := regexp.MustCompile(`wake_cursor: [0-9]+`).FindString(h.ok("supervisor", "show"))
	h.ok("supervisor", "stop")
	has(t, "resume", h.ok("supervisor", "resume"), "supervisor: s2", "status: running", "session: "+session)
	has(t, "show", h.ok("supervisor", "show"), "supervisor: s2", "resumes: s1")
	if !strings.Contains(h.ok("orient"), "supervisor: s2 claude running (resumes s1)") {
		t.Fatal("orient must name the live supervisor and the ref its session's launch prompt used")
	}
	argv := rt.lastCreate().Command
	if !slices.Equal(argv[1:], []string{"--dangerously-skip-permissions", "--resume", session, "--model", "sonnet", "--effort", "low"}) {
		t.Fatalf("argv = %q", argv)
	}
	has(t, "show", h.ok("supervisor", "show"), cursor)
	if _, _, code := h.run("supervisor", "resume"); code != 3 {
		t.Fatalf("resume while live code=%d", code)
	}
}

func TestARestoredSupervisorTerminalIsClosed(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	session := rt.lastCreate().Command[3]
	pid := rt.lastPID()
	rt.srv.SetGeneration("gen-2")
	stray := rt.addShell(t, h.home, "hand-supervisor")
	restored := rt.addShell(t, h.home, "")
	rt.setSession(restored, session)
	operator := rt.addShell(t, h.home, "")
	rt.setSession(operator, "0d7a5c6e-6b1f-4f4e-9a57-2b8f0c1d2e3f")
	has(t, "show", h.ok("supervisor", "show"), "status: interrupted", "reason: luvus server restarted")
	if !rt.isClosed(stray) || !rt.isClosed(restored) {
		t.Fatalf("restored supervisor terminals left open: labelled %v, by session %v", !rt.isClosed(stray), !rt.isClosed(restored))
	}
	if !gone(pid) {
		t.Fatalf("supervisor %d survived the restart", pid)
	}
	has(t, "resume", h.ok("supervisor", "resume"), "supervisor: s2", "status: running")
	late := rt.addShell(t, h.home, "hand-supervisor")
	lateRestored := rt.addShell(t, h.home, "")
	rt.setSession(lateRestored, session)
	has(t, "show", h.ok("supervisor", "show"), "supervisor: s2", "status: running")
	if !rt.isClosed(late) || !rt.isClosed(lateRestored) {
		t.Fatal("a stray supervisor terminal next to the live one was left open")
	}
	h.ok("supervisor", "stop")
	startClaudeSupervisor(h)
	older := rt.addShell(t, h.home, "")
	rt.setSession(older, session)
	has(t, "show", h.ok("supervisor", "show"), "supervisor: s3", "status: running")
	if !rt.isClosed(older) {
		t.Fatal("a restored terminal of an earlier supervisor session was left open")
	}
	if rt.isClosed(operator) {
		t.Fatal("closed the operator's own session in the fleet home")
	}
}

func TestDeliveryWaitsForTheHarnessSession(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	codexHome := filepath.Join(h.vars["HOME"], ".codex")
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "models_cache.json"), []byte(`{"models":[{"slug":"gpt-6-luna","supported_reasoning_levels":[{"effort":"low"}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	has(t, "start", h.ok("supervisor", "start", "--harness", "codex", "--model", "gpt-6-luna", "--effort", "low"), "supervisor: s1", `session: ""`)
	has(t, "send", h.ok("supervisor", "send", "--text", "first"), "delivered: no", "why: ")
	if got := rt.prompts(); len(got) != 0 {
		t.Fatalf("sent to a codex supervisor with no session yet (a trust screen reads as idle): %q", got)
	}
	now := time.Now()
	dir := filepath.Join(codexHome, "sessions", now.Format("2006"), now.Format("01"), now.Format("02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd, err := filepath.EvalSymlinks(h.home)
	if err != nil {
		t.Fatal(err)
	}
	meta := `{"type":"session_meta","payload":{"id":"codex-session-1","cwd":"` + cwd + `","timestamp":"` + now.UTC().Format(time.RFC3339Nano) + `"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"You are supervisor s1 of the Hand fleet x."}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "rollout-1.jsonl"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	has(t, "send", h.ok("supervisor", "send", "--text", "second"), "delivered: yes")
	if got := rt.prompts(); !slices.Equal(got, []string{"first", "second"}) {
		t.Fatalf("prompts = %q", got)
	}
	has(t, "show", h.ok("supervisor", "show"), "session: codex-session-1")
}

func TestSendToAStaleSupervisorIsHeld(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	lock, err := os.OpenFile(filepath.Join(h.home, "locks", "supervisor.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := flock.Lock(lock, true); err != nil {
		t.Fatal(err)
	}
	rt.srv.SetGeneration("gen-2")
	done := make(chan string, 1)
	go func() {
		out, errOut, _ := h.run("supervisor", "send", "--text", "hello")
		done <- out + errOut
	}()
	time.Sleep(300 * time.Millisecond)
	_ = lock.Close()
	has(t, "send", <-done, "delivered: no")
	if got := rt.prompts(); len(got) != 0 {
		t.Fatalf("sent to a supervisor of an earlier server: %q", got)
	}
	has(t, "show", h.ok("supervisor", "show"), "status: interrupted", "pending: 1")
}

func TestAttemptCommandsSurviveSupervisorHousekeepingFailures(t *testing.T) {
	fx := newAttemptFixture(t)
	startClaudeSupervisor(fx.h)
	fx.h.ok("supervisor", "stop")
	fx.start()
	fx.rt.srv.Handle("terminal.backend.inventory", func(json.RawMessage) (any, error) {
		return nil, fakeuhp.Fail{Code: "unavailable", Message: "inventory is unavailable"}
	})
	has(t, "show", fx.h.ok("attempt", "show", "a1"), "status: running")
}

func TestARefusedPromptDoesNotStopTheWatcher(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	rt.srv.Handle("agent.prompt", func(json.RawMessage) (any, error) {
		return nil, fakeuhp.Fail{Code: "invalid_params", Message: "text is too long"}
	})
	has(t, "send", h.ok("supervisor", "send", "--text", "hello"), "delivered: no", "text is too long")
	stop := startWatch(t, &attemptFixture{h: h, rt: rt}, "--every", "20ms")
	time.Sleep(400 * time.Millisecond)
	stop()
	if n := len(rt.srv.Calls("events.subscribe")); n != 1 {
		t.Fatalf("watcher reconnected %d times over a refused prompt", n-1)
	}
}

func TestSupervisorKeysAreLimited(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	has(t, "keys", h.ok("supervisor", "keys", "--revision", "0", "enter"), "keys: enter")
	if _, errOut, code := h.run("supervisor", "keys", "--revision", "0", "ctrl+c"); code != 2 || !strings.Contains(errOut, "ctrl+c") {
		t.Fatalf("ctrl+c code=%d stderr=%q", code, errOut)
	}
	if got := rt.keysSent(); !slices.Equal(got, []string{"enter"}) {
		t.Fatalf("keys = %q", got)
	}
}

func TestOpencodeSupervisorPromptIsSubmitted(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	script := "#!/bin/sh\nif [ \"$1\" = session ]; then echo '[]'; exit 0; fi\nexec sleep 300\n"
	if err := os.WriteFile(filepath.Join(h.vars["PATH"], "opencode"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	rt.set(func(rt *fakeRuntime) {
		rt.screen, rt.revision, rt.status, rt.afterKeys = "┃  You are supervisor s1 of the Hand fleet", 7, "idle", "working"
	})
	has(t, "start", h.ok("supervisor", "start", "--harness", "opencode"), "supervisor: s1", "prompt: submitted")
	argv := rt.lastCreate().Command
	if len(argv) != 5 || !slices.Equal(argv[1:4], []string{"--standalone", "--auto", "--prompt"}) || !launchPrompt.MatchString(argv[4]) {
		t.Fatalf("argv = %q", argv)
	}
	if got := rt.keysSent(); !slices.Equal(got, []string{"enter"}) {
		t.Fatalf("keys = %q", got)
	}
	has(t, "show", h.ok("supervisor", "show"), "status: running", "agent: working")
}

func supervisorPane(t *testing.T, out string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^pane: (\S+)$`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no pane in %q", out)
	}
	return m[1]
}

func explains(rt *fakeRuntime) int { return len(rt.srv.Calls("agent.explain")) }

func publishStatus(fx *attemptFixture, pane, status string) {
	fx.rt.set(func(rt *fakeRuntime) { rt.status = status })
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": pane, "status": status, "agent": "claude"})
}

func openStore(t *testing.T, h *harness) *state.Store {
	t.Helper()
	st, err := state.Open(filepath.Join(h.home, "hand.db"), func() time.Time { return h.now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSendDeliversAtOnceEvenWhileWorking(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	out := h.ok("supervisor", "send", "--text", "Plan the login fix")
	has(t, "send", out, "input: i1", "delivered: yes")
	if strings.Contains(out, "why") {
		t.Fatalf("send = %q", out)
	}
	if got := rt.prompts(); !slices.Equal(got, []string{"Plan the login fix"}) {
		t.Fatalf("prompts = %q", got)
	}
	has(t, "show", h.ok("supervisor", "show"), "pending: 0")
}

func TestSendWaitsWhileBlocked(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	fx := &attemptFixture{h: h, rt: rt}
	pane := supervisorPane(t, startClaudeSupervisor(h))
	rt.set(func(rt *fakeRuntime) { rt.status, rt.hint = "blocked", "Trust this folder?" })
	has(t, "send", h.ok("supervisor", "send", "--text", "hello"), "delivered: no", `why: "supervisor blocked: Trust this folder?"`)
	before := explains(rt)
	stop := startWatch(t, fx, "--every", "1h")
	eventually(t, func() bool { return explains(rt) > before })
	if got := rt.prompts(); len(got) != 0 {
		t.Fatalf("delivered while blocked: %q", got)
	}
	publishStatus(fx, pane, "idle")
	eventually(t, func() bool { return slices.Equal(rt.prompts(), []string{"hello"}) })
	stop()
	has(t, "show", h.ok("supervisor", "show"), "pending: 0")
}

func TestWakesWaitForIdleAndCarryTheEnvelope(t *testing.T) {
	fx := newAttemptFixture(t)
	pane := supervisorPane(t, startClaudeSupervisor(fx.h))
	fx.start()
	stop := startWatch(t, fx, "--every", "1h")
	eventually(t, func() bool { return explains(fx.rt) > 0 })
	fx.h.ok("report", "add", "--attempt", "a1", "--status", "done", "--text", "Fixed login")
	n := explains(fx.rt)
	publishStatus(fx, pane, "working")
	eventually(t, func() bool { return explains(fx.rt) > n })
	if got := fx.rt.prompts(); len(got) != 0 {
		t.Fatalf("digest sent while working: %q", got)
	}
	publishStatus(fx, pane, "idle")
	eventually(t, func() bool { return len(fx.rt.prompts()) == 1 })
	stop()
	wake := fx.rt.prompts()[0]
	if !strings.HasPrefix(wake, "[hand v1 wake]\n") || !strings.Contains(wake, "\nattempt.reported a1: r1 done") {
		t.Fatalf("wake = %q", wake)
	}
	events, err := openStore(t, fx.h).EventsAfter(context.Background(), 0, []string{"attempt.reported"}, 1)
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %v, %v", events, err)
	}
	has(t, "show", fx.h.ok("supervisor", "show"), fmt.Sprintf("wake_cursor: %d", events[0].Seq))
}

func TestWakeCursorStopsAtWhatWasSent(t *testing.T) {
	fx := newAttemptFixture(t)
	pane := supervisorPane(t, startClaudeSupervisor(fx.h))
	fx.start()
	fx.h.ok("attempt", "stop", "a1")
	st := openStore(t, fx.h)
	ctx := context.Background()
	for i := range 60 {
		if err := st.NoteAttempt(ctx, 1, "reported", fmt.Sprintf("q%02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	events, err := st.EventsAfter(ctx, 0, []string{"attempt.reported"}, 100)
	if err != nil || len(events) != 60 {
		t.Fatalf("events = %d, %v", len(events), err)
	}
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	stop := startWatch(t, fx, "--every", "1h")
	eventually(t, func() bool { return len(fx.rt.prompts()) == 1 })
	first := strings.Split(fx.rt.prompts()[0], "\n")
	if len(first) != 51 || first[0] != "[hand v1 wake]" || first[1] != "attempt.reported a1: q00 (t1 \"Fix login\")" || first[50] != "attempt.reported a1: q49 (t1 \"Fix login\")" {
		t.Fatalf("first digest = %d lines: %q ... %q", len(first), first[0], first[len(first)-1])
	}
	eventually(t, func() bool {
		return strings.Contains(fx.h.ok("supervisor", "show"), fmt.Sprintf("wake_cursor: %d\n", events[49].Seq))
	})
	publishStatus(fx, pane, "idle")
	eventually(t, func() bool { return len(fx.rt.prompts()) == 2 })
	eventually(t, func() bool {
		return strings.Contains(fx.h.ok("supervisor", "show"), fmt.Sprintf("wake_cursor: %d\n", events[59].Seq))
	})
	stop()
	second := strings.Split(fx.rt.prompts()[1], "\n")
	if len(second) != 11 || second[1] != "attempt.reported a1: q50 (t1 \"Fix login\")" || second[10] != "attempt.reported a1: q59 (t1 \"Fix login\")" {
		t.Fatalf("second digest = %q", second)
	}
}

func TestPendingMessagesSurviveAnInterruptedSupervisor(t *testing.T) {
	fx := newAttemptFixture(t)
	startClaudeSupervisor(fx.h)
	fx.start()
	fx.rt.set(func(rt *fakeRuntime) { rt.status, rt.hint = "blocked", "Pick one" })
	has(t, "send", fx.h.ok("supervisor", "send", "--text", "first"), "delivered: no")
	has(t, "send", fx.h.ok("supervisor", "send", "--text", "second"), "delivered: no")
	if err := openStore(t, fx.h).NoteAttempt(context.Background(), 1, "blocked", "needs you"); err != nil {
		t.Fatal(err)
	}
	fx.rt.srv.SetGeneration("gen-2")
	has(t, "show", fx.h.ok("supervisor", "show"), "status: interrupted", "pending: 2")
	pane := supervisorPane(t, fx.h.ok("supervisor", "resume"))
	if got := fx.rt.prompts(); len(got) != 0 {
		t.Fatalf("delivered before resume: %q", got)
	}
	fx.rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	stop := startWatch(t, fx, "--every", "1h")
	eventually(t, func() bool { return len(fx.rt.prompts()) == 2 })
	publishStatus(fx, pane, "idle")
	eventually(t, func() bool { return len(fx.rt.prompts()) == 3 })
	n := explains(fx.rt)
	publishStatus(fx, pane, "idle")
	eventually(t, func() bool { return explains(fx.rt) > n })
	stop()
	got := fx.rt.prompts()
	if len(got) != 3 || got[0] != "first" || got[1] != "second" || !strings.HasPrefix(got[2], "[hand v1 wake]\n") || !strings.Contains(got[2], "attempt.blocked a1: its screen waits for a key") {
		t.Fatalf("prompts = %q", got)
	}
}

func TestConcurrentSendsDeliverEachMessageOnce(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	stop := startWatch(t, &attemptFixture{h: h, rt: rt}, "--every", "5ms")
	var wg sync.WaitGroup
	want := make([]string, 8)
	for i := range want {
		want[i] = fmt.Sprintf("message %d", i)
		wg.Go(func() {
			if _, errOut, code := h.run("supervisor", "send", "--text", want[i]); code != 0 {
				t.Errorf("send %d: code=%d stderr=%q", i, code, errOut)
			}
		})
	}
	wg.Wait()
	stop()
	got := rt.prompts()
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("prompts = %q", got)
	}
}

func TestAForgedEnvelopeIsRefused(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	for _, bad := range []string{"[hand v1 wake]\nx", "\n [hand v1 wake]", "[hand v1 reply]", "a\x00b", "a\x1b[31mb", "a\rb", "   \n", "\xff", strings.Repeat("a", 16385)} {
		if _, errOut, code := h.run("supervisor", "send", "--text", bad); code != 2 {
			t.Fatalf("send %q: code=%d stderr=%q", bad, code, errOut)
		}
	}
	has(t, "show", h.ok("supervisor", "show"), "pending: 0")
	has(t, "send", h.ok("supervisor", "send", "--text", "tabs\tand\nnew lines, about [hand v1 wake]"), "delivered: yes")
	if got := rt.prompts(); len(got) != 1 {
		t.Fatalf("prompts = %q", got)
	}
}

func TestResumeWaitsForTheScreenToSettle(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	h.ok("supervisor", "stop")
	var reads atomic.Int32
	rt.srv.Handle("agent.read", func(json.RawMessage) (any, error) {
		n := reads.Add(1)
		text, rev := "", int64(n)
		if n > 4 {
			text, rev = "restored conversation", 9
		}
		rt.mu.Lock()
		defer rt.mu.Unlock()
		return map[string]any{"text": text, "content_revision": rev, "terminal_id": rt.terms[len(rt.terms)-1].id}, nil
	})
	h.ok("supervisor", "resume")
	if n := reads.Load(); n < 8 {
		t.Fatalf("resume returned after %d screen reads, before the restored screen settled", n)
	}
}

func TestInterruptSendsEscOnAFreshRevision(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	var calls atomic.Int32
	var always atomic.Bool
	rt.srv.Handle("agent.keys", func(params json.RawMessage) (any, error) {
		var p struct {
			Keys     []string `json:"keys"`
			Revision int64    `json:"if_content_revision"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		n := calls.Add(1)
		rt.mu.Lock()
		defer rt.mu.Unlock()
		if always.Load() || n == 1 {
			rt.revision++
			return nil, fakeuhp.Fail{Code: "content_revision_conflict", Message: "moved"}
		}
		if p.Revision != rt.revision {
			return nil, fakeuhp.Fail{Code: "content_revision_conflict", Message: "stale"}
		}
		rt.keyed = append(rt.keyed, p.Keys...)
		return map[string]any{"type": "ok"}, nil
	})
	has(t, "interrupt", h.ok("supervisor", "interrupt"), "supervisor: s1", "keys: esc")
	if got := rt.keysSent(); !slices.Equal(got, []string{"esc"}) || calls.Load() != 2 {
		t.Fatalf("keys = %q after %d calls", got, calls.Load())
	}
	always.Store(true)
	if _, errOut, code := h.run("supervisor", "interrupt"); code != 3 || !strings.Contains(errOut, "nothing was sent") {
		t.Fatalf("always stale: code=%d stderr=%q", code, errOut)
	}
}

func TestControlRunsTheCLIAndMapsItsErrors(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	var out, errOut bytes.Buffer
	control := cli.Control(h.env(&out, &errOut), h.home)
	ctx := context.Background()
	if err := control(ctx, "supervisor", "resume"); !errors.Is(err, state.ErrNotFound) && !errors.Is(err, state.ErrConflict) {
		t.Fatalf("resume with none = %v", err)
	}
	if err := control(ctx, "supervisor", "start", "--harness", "codex", "--model", "gpt-6-luna", "--effort", "low"); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), "models_cache.json") || strings.Contains(err.Error(), h.vars["HOME"]) {
		t.Fatalf("path in error = %v", err)
	}
	startClaudeSupervisor(h)
	if err := control(ctx, "supervisor", "send", "--text", "hi"); err != nil {
		t.Fatal(err)
	}
	if got := rt.prompts(); !slices.Equal(got, []string{"hi"}) {
		t.Fatalf("prompts = %q", got)
	}
	if err := control(ctx, "supervisor", "keys", "--revision", "0", "ctrl+c"); !errors.Is(err, state.ErrInvalid) {
		t.Fatalf("bad key = %v", err)
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("control wrote to the caller's streams: %q %q", out.String(), errOut.String())
	}
}

func opencodeSupervisor(t *testing.T, h *harness, rt *fakeRuntime, keys func(n int32)) {
	t.Helper()
	script := "#!/bin/sh\nif [ \"$1\" = session ]; then echo '[]'; exit 0; fi\nexec sleep 300\n"
	if err := os.WriteFile(filepath.Join(h.vars["PATH"], "opencode"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	rt.set(func(rt *fakeRuntime) {
		rt.screen, rt.revision, rt.status = "┃  You are supervisor s1 of the Hand fleet", 7, "idle"
	})
	var calls atomic.Int32
	rt.srv.Handle("agent.keys", func(params json.RawMessage) (any, error) {
		var p struct {
			Keys []string `json:"keys"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		rt.mu.Lock()
		rt.keyed = append(rt.keyed, strings.Join(p.Keys, "+"))
		rt.mu.Unlock()
		keys(calls.Add(1))
		return map[string]any{"type": "ok"}, nil
	})
}

func TestOpencodeEnterIsRetriedWhileItsScreenLoads(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	opencodeSupervisor(t, h, rt, func(n int32) {
		if n == 2 {
			rt.set(func(rt *fakeRuntime) { rt.status = "working" })
		}
	})
	has(t, "start", h.ok("supervisor", "start", "--harness", "opencode"), "prompt: submitted")
	if got := rt.keysSent(); !slices.Equal(got, []string{"enter", "enter"}) {
		t.Fatalf("keys = %q", got)
	}
}

func TestOpencodeInterruptPressesEscTwice(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	opencodeSupervisor(t, h, rt, func(int32) { rt.set(func(rt *fakeRuntime) { rt.status = "working" }) })
	h.ok("supervisor", "start", "--harness", "opencode")
	has(t, "interrupt", h.ok("supervisor", "interrupt"), "keys: esc esc")
	if got := rt.keysSent(); !slices.Equal(got, []string{"enter", "esc", "esc"}) {
		t.Fatalf("keys = %q, want two separate fenced esc presses: opencode reads esc+esc in one write as one sequence", got)
	}
}

func resumedArgv(t *testing.T, rt *fakeRuntime, session, model, effort string) {
	t.Helper()
	argv := rt.lastCreate().Command
	if !slices.Equal(argv[1:], []string{"--dangerously-skip-permissions", "--resume", session, "--model", model, "--effort", effort}) {
		t.Fatalf("argv = %q", argv)
	}
}

func TestSwitchWhileIdleRelaunchesTheSession(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	session := rt.lastCreate().Command[3]
	cursor := regexp.MustCompile(`wake_cursor: [0-9]+`).FindString(h.ok("supervisor", "show"))
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	has(t, "switch", h.ok("supervisor", "switch", "--model", "opus", "--effort", "high"), "supervisor: s2", "switch: applied", "model: opus", "effort: high")
	resumedArgv(t, rt, session, "opus", "high")
	has(t, "show", h.ok("supervisor", "show"), "supervisor: s2", "resumes: s1", "harness: claude opus high", "status: running", cursor)
	events, err := openStore(t, h).EventsAfter(context.Background(), 0, []string{"supervisor.stopped"}, 10)
	if err != nil || len(events) != 1 || events[0].Detail != "s1: switched to opus high" {
		t.Fatalf("stop events = %+v, %v", events, err)
	}
}

func TestSwitchWhileWorkingWaitsForTheTurn(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	fx := &attemptFixture{h: h, rt: rt}
	pane := supervisorPane(t, startClaudeSupervisor(h))
	session := rt.lastCreate().Command[3]
	why := "why: switching to opus high after this turn"
	has(t, "switch", h.ok("supervisor", "switch", "--model", "opus", "--effort", "high"), "supervisor: s1", "switch: pending", why, "hand supervisor switch --cancel")
	has(t, "send one", h.ok("supervisor", "send", "--text", "one"), "delivered: no", why)
	has(t, "send two", h.ok("supervisor", "send", "--text", "two"), "delivered: no", why)
	if got := rt.prompts(); len(got) != 0 {
		t.Fatalf("delivered while a switch waits: %q", got)
	}
	stop := startWatch(t, fx, "--every", "1h")
	publishStatus(fx, pane, "idle")
	eventually(t, func() bool { return slices.Equal(rt.prompts(), []string{"one", "two"}) })
	stop()
	resumedArgv(t, rt, session, "opus", "high")
	has(t, "show", h.ok("supervisor", "show"), "supervisor: s2", "pending: 0")
}

func TestSwitchCanBeCanceled(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	h.ok("supervisor", "switch", "--model", "opus", "--effort", "high")
	has(t, "show", h.ok("supervisor", "show"), "switch: opus high")
	has(t, "cancel", h.ok("supervisor", "switch", "--cancel"), "supervisor: s1", "switch: canceled")
	if out := h.ok("supervisor", "show"); strings.Contains(out, "switch:") {
		t.Fatalf("show after cancel = %q", out)
	}
	if _, _, code := h.run("supervisor", "switch", "--cancel"); code != 3 {
		t.Fatalf("second cancel code = %d", code)
	}
}

func TestSwitchRefusals(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	if _, _, code := h.run("supervisor", "switch", "--model", "opus"); code != 3 {
		t.Fatalf("no supervisor code = %d", code)
	}
	policy := `{"profiles":{"deep":{"harness":"claude","model":"opus","effort":"high"},"luna":{"harness":"codex","model":"gpt-6-luna","effort":"low"}}}`
	if err := os.WriteFile(filepath.Join(h.home, "routing.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	startClaudeSupervisor(h)
	for _, c := range []struct {
		args []string
		code int
		want string
	}{
		{nil, 2, "switch"},
		{[]string{"--profile", "deep", "--model", "opus"}, 2, "--profile"},
		{[]string{"--profile", "luna"}, 2, "models_cache.json"},
		{[]string{"--model", "sonnet", "--effort", "low"}, 3, "already"},
		{[]string{"--effort", "huge"}, 2, "effort"},
	} {
		_, errOut, code := h.run(append([]string{"supervisor", "switch"}, c.args...)...)
		if code != c.code || !strings.Contains(errOut, c.want) {
			t.Fatalf("switch %q: code=%d stderr=%q", c.args, code, errOut)
		}
	}
	has(t, "profile", h.ok("supervisor", "switch", "--profile", "deep"), "switch: pending", "model: opus")
	if _, errOut, code := h.run("supervisor", "switch", "--effort", "max"); code != 3 || !strings.Contains(errOut, "--cancel") {
		t.Fatalf("second switch code=%d stderr=%q", code, errOut)
	}
}

func TestSwitchRefusesOpencode(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	opencodeSupervisor(t, h, rt, func(int32) {})
	h.ok("supervisor", "start", "--harness", "opencode")
	if _, errOut, code := h.run("supervisor", "switch", "--model", "x"); code != 2 || !strings.Contains(errOut, "opencode keeps its model") {
		t.Fatalf("opencode switch code=%d stderr=%q", code, errOut)
	}
}

func TestResumeHonoursAPendingSwitch(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	session := rt.lastCreate().Command[3]
	has(t, "switch", h.ok("supervisor", "switch", "--model", "opus", "--effort", "high"), "switch: pending")
	h.ok("supervisor", "stop")
	has(t, "resume", h.ok("supervisor", "resume"), "supervisor: s2")
	resumedArgv(t, rt, session, "opus", "high")
}

func TestSwitchWithOnlyEffortKeepsTheModel(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	session := rt.lastCreate().Command[3]
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	has(t, "switch", h.ok("supervisor", "switch", "--effort", "high"), "switch: applied", "model: sonnet", "effort: high")
	resumedArgv(t, rt, session, "sonnet", "high")
}

func TestCancelDuringAnApplyWaitsForIt(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	has(t, "switch", h.ok("supervisor", "switch", "--model", "opus", "--effort", "high"), "switch: pending")
	rt.set(func(rt *fakeRuntime) { rt.status, rt.closeDelay = "idle", 1500*time.Millisecond })
	done := make(chan int, 1)
	go func() {
		_, _, code := h.run("supervisor", "send", "--text", "hello")
		done <- code
	}()
	time.Sleep(700 * time.Millisecond)
	if _, errOut, code := h.run("supervisor", "switch", "--cancel"); code != 3 || !strings.Contains(errOut, "no pending switch") {
		t.Fatalf("cancel during the apply code=%d stderr=%q", code, errOut)
	}
	if code := <-done; code != 0 {
		t.Fatalf("send during the apply code = %d", code)
	}
	has(t, "show", h.ok("supervisor", "show"), "supervisor: s2", "harness: claude opus high", "status: running", "pending: 0")
}

func TestAFailedRelaunchSaysHowToRecover(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	rt.srv.Handle("terminal.backend.create", func(json.RawMessage) (any, error) {
		return nil, fakeuhp.Fail{Code: "spawn_failed", Message: "no pty"}
	})
	_, errOut, code := h.run("supervisor", "switch", "--model", "opus", "--effort", "high")
	if code == 0 || !strings.Contains(errOut, "hand supervisor resume") {
		t.Fatalf("failed relaunch code=%d stderr=%q", code, errOut)
	}
}

func TestAnUnknownRelaunchDoesNotPromiseResume(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	rt.srv.Handle("terminal.backend.create", func(json.RawMessage) (any, error) { return nil, fakeuhp.Drop })
	_, errOut, code := h.run("supervisor", "switch", "--model", "opus", "--effort", "high")
	if code == 0 || !strings.Contains(errOut, "stays launching") || strings.Contains(errOut, "hand supervisor resume") {
		t.Fatalf("unknown relaunch code=%d stderr=%q", code, errOut)
	}
}

func TestSupervisorForceTypesQueuedMessagesIntoAMisreadScreen(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	rt.set(func(rt *fakeRuntime) {
		rt.status, rt.hint, rt.revision, rt.ready = "blocked", "⏵⏵ bypass permissions on · ⧉ players", 9, false
		rt.screen = "✻ Worked for 11s\n────\n❯\u00a0\n────\n  ⏵⏵ bypass permissions on · ⧉ players"
	})
	has(t, "send", h.ok("supervisor", "send", "--text", "1. MIT\n2. yes"), "delivered: no")
	has(t, "force", h.ok("supervisor", "force"), "typed: 1")
	want := []string{"1", ".", " ", "M", "I", "T", "\\", "enter", "2", ".", " ", "y", "e", "s", "enter"}
	if got := rt.keysSent(); !slices.Equal(got, want) {
		t.Fatalf("keys = %q, want %q", got, want)
	}
	has(t, "show", h.ok("supervisor", "show"), "pending: 0")
	if events, err := openStore(t, h).EventsAfter(context.Background(), 0, []string{"supervisor.delivered"}, 10); err != nil || len(events) != 1 || events[0].Detail != "i1: typed" {
		t.Fatalf("deliveries = %+v, %v", events, err)
	}
	if len(rt.prompts()) != 0 {
		t.Fatalf("force pasted %q instead of typing", rt.prompts())
	}
}

func TestSupervisorForceTypesTheWakesWhenNoMessageWaits(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	h.ok("project", "add", "hand", "/home/me/hand")
	h.ok("task", "add", "hand", "Fix login")
	h.ok("decision", "ask", "t1", "Keep it?")
	h.ok("decision", "answer", "d1", "yes")
	rt.set(func(rt *fakeRuntime) {
		rt.status, rt.revision, rt.ready, rt.screen = "blocked", 4, false, "────\n❯ \n────"
	})
	has(t, "force", h.ok("supervisor", "force"), "typed: 1")
	typed := strings.ReplaceAll(strings.Join(rt.keysSent(), ""), "\\enter", "\n")
	if !strings.HasPrefix(typed, "[hand v1 wake]\ndecision.answered d1") || !strings.HasSuffix(typed, "enter") {
		t.Fatalf("typed = %q", typed)
	}
	has(t, "force again", h.ok("supervisor", "force"), "typed: 0")
}

func TestSupervisorForceNeverTypesIntoARealQuestion(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	rt.set(func(rt *fakeRuntime) { rt.status, rt.hint, rt.ready = "blocked", "Do you want to proceed?", false })
	h.ok("supervisor", "send", "--text", "1")
	if _, errOut, code := h.run("supervisor", "force"); code != 3 || !strings.Contains(errOut, "no empty prompt") {
		t.Fatalf("force on a real question: code=%d stderr=%q", code, errOut)
	}
	if got := rt.keysSent(); len(got) != 0 {
		t.Fatalf("force typed %q into a real question", got)
	}
	has(t, "show", h.ok("supervisor", "show"), "pending: 1")
}

func TestSupervisorForceRefusesAPaneThatNoLongerHoldsTheSupervisor(t *testing.T) {
	fx := newAttemptFixture(t)
	startClaudeSupervisor(fx.h)
	fx.start()
	fx.rt.set(func(rt *fakeRuntime) {
		rt.status, rt.revision, rt.ready, rt.screen = "blocked", 3, false, "────\n❯ \n────"
	})
	fx.h.ok("supervisor", "send", "--text", "hello")
	if _, errOut, code := fx.h.run("supervisor", "force"); code != 3 || !strings.Contains(errOut, "no longer holds") {
		t.Fatalf("force into another terminal: code=%d stderr=%q", code, errOut)
	}
	if got := fx.rt.keysSent(); len(got) != 0 {
		t.Fatalf("force typed %q into another terminal", got)
	}
	has(t, "show", fx.h.ok("supervisor", "show"), "pending: 1")
}

const (
	promptEarly = "Bash command\n  rm -f $r/$f\n Claude Code will automatically deny this request in 1:59\n Do you want to proceed?\n❯ 1. Yes\n  2. No"
	promptLate  = "Bash command\n  rm -f $r/$f\n Claude Code will automatically deny this request in 1:12\n Do you want to proceed?\n❯ 1. Yes\n  2. No"
	promptOther = "Edit file\n Claude Code will automatically deny this request in 1:12\n Do you want to make this edit?\n❯ 1. Yes\n  2. No"
)

func TestSupervisorKeysRetryWhenOnlyTheCountdownMoved(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	rt.set(func(rt *fakeRuntime) { rt.status, rt.revision, rt.screen = "blocked", 8, promptLate })
	has(t, "keys", h.ok("supervisor", "keys", "--revision", "7", "--screen", luvus.ScreenDigest(promptEarly), "2"), "keys: 2")
	if got := rt.keysSent(); !slices.Equal(got, []string{"2"}) {
		t.Fatalf("keys = %q", got)
	}
}

func TestSupervisorKeysRefuseWhenTheQuestionChanged(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	rt.set(func(rt *fakeRuntime) { rt.status, rt.revision, rt.screen = "blocked", 8, promptOther })
	_, errOut, code := h.run("supervisor", "keys", "--revision", "7", "--screen", luvus.ScreenDigest(promptEarly), "2")
	if code != 3 || !strings.Contains(errOut, "the screen changed") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if got := rt.keysSent(); len(got) != 0 {
		t.Fatalf("keys = %q", got)
	}
}

func TestSupervisorKeysAreRecorded(t *testing.T) {
	h, _ := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	h.ok("supervisor", "keys", "--revision", "0", "enter")
	events, err := openStore(t, h).EventsAfter(context.Background(), 0, []string{"supervisor.keys"}, 10)
	if err != nil || len(events) != 1 || events[0].Detail != "s1: enter" {
		t.Fatalf("events = %+v, %v", events, err)
	}
}

func TestWakesNameTheTaskOnOneLine(t *testing.T) {
	title := func(int64) string { return "Fix \"login\"\nnow" }
	digest, last := cli.WakeDigest([]state.Event{{Seq: 3, Kind: "attempt.reported", TaskID: 1, Detail: "a1: r1 done"}, {Seq: 4, Kind: "decision.answered", Detail: "d1"}}, title)
	want := "[hand v1 wake]\nattempt.reported a1: r1 done (t1 \"Fix 'login' now\")\ndecision.answered d1"
	if digest != want || last != 4 {
		t.Fatalf("digest = %q (last %d), want %q", digest, last, want)
	}
	var many []state.Event
	for i := range 50 {
		many = append(many, state.Event{Seq: int64(i + 1), Kind: "attempt.blocked", TaskID: 1, Detail: "a1: " + strings.Repeat("q", 400)})
	}
	long := func(int64) string { return strings.Repeat("t", 200) }
	if digest, _ := cli.WakeDigest(many, long); len(digest) > hh.MaxPromptBytes || strings.Count(digest, "\n") < 1 || !strings.Contains(digest, strings.Repeat("t", 60)+"…") || strings.Contains(digest, strings.Repeat("t", 61)) {
		t.Fatalf("a long digest is %d bytes", len(digest))
	}
}

func codexCache(t *testing.T, h *harness) {
	t.Helper()
	dir := filepath.Join(h.vars["HOME"], ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models_cache.json"), []byte(`{"models":[{"slug":"gpt-6-luna","supported_reasoning_levels":[{"effort":"low"}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSwitchToAnotherHarnessStartsTheNextSupervisor(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	codexCache(t, h)
	startClaudeSupervisor(h)
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	has(t, "switch", h.ok("supervisor", "switch", "--harness", "codex", "--model", "gpt-6-luna", "--effort", "low"), "supervisor: s2", "harness: codex")
	show := h.ok("supervisor", "show")
	has(t, "show", show, "supervisor: s2", "harness: codex gpt-6-luna low")
	argv := rt.lastCreate().Command
	if prompt := argv[len(argv)-1]; !strings.Contains(prompt, "You replace s1, which ran on claude;") {
		t.Fatalf("launch message = %q", prompt)
	}
	events, err := openStore(t, h).EventsAfter(context.Background(), 0, []string{"supervisor.stopped"}, 10)
	if err != nil || len(events) != 1 || events[0].Detail != "s1: switched to codex" {
		t.Fatalf("stop events = %+v, %v", events, err)
	}
}

func TestSwitchingHarnessIsRefusedWhileWorking(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	codexCache(t, h)
	startClaudeSupervisor(h)
	rt.set(func(rt *fakeRuntime) { rt.status = "working" })
	if _, errOut, code := h.run("supervisor", "switch", "--harness", "codex", "--model", "gpt-6-luna", "--effort", "low"); code != 3 || !strings.Contains(errOut, "s1 is working; switch harness when its turn ends, or interrupt it first") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	has(t, "show", h.ok("supervisor", "show"), "supervisor: s1", "status: running")
}

func TestBlockedWakesLeaveOutTheScreenHint(t *testing.T) {
	title := func(int64) string { return "Fix login" }
	digest, _ := cli.WakeDigest([]state.Event{{Seq: 1, Kind: "attempt.blocked", TaskID: 1, Detail: "a1: Enter to confirm · Esc to cancel"}}, title)
	if want := "[hand v1 wake]\nattempt.blocked a1: its screen waits for a key (t1 \"Fix login\")"; digest != want {
		t.Fatalf("digest = %q, want %q", digest, want)
	}
}

func TestSwitchingHarnessKeepsTheSupervisorWhenItCannotStartTheNext(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	codexCache(t, h)
	startClaudeSupervisor(h)
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	if err := os.Remove(filepath.Join(h.vars["PATH"], "codex")); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := h.run("supervisor", "switch", "--harness", "codex", "--model", "gpt-6-luna", "--effort", "low"); code == 0 || !strings.Contains(errOut, "codex") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	has(t, "show", h.ok("supervisor", "show"), "supervisor: s1", "status: running")
	rt.set(func(rt *fakeRuntime) { rt.status = "blocked" })
	if _, errOut, code := h.run("supervisor", "switch", "--harness", "opencode"); code != 3 || !strings.Contains(errOut, "s1 is blocked") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	has(t, "show", h.ok("supervisor", "show"), "supervisor: s1", "status: running")
}

func TestSupervisorKeysRetryOnlyDuringACountdown(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	still := "Bash command\n  rm -f $r/$f\n Do you want to proceed?\n❯ 1. Yes\n  2. No"
	rt.set(func(rt *fakeRuntime) { rt.status, rt.revision, rt.screen = "blocked", 8, still })
	if _, errOut, code := h.run("supervisor", "keys", "--revision", "7", "--screen", luvus.ScreenDigest(still), "2"); code != 3 || !strings.Contains(errOut, "the screen changed") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if got := rt.keysSent(); len(got) != 0 {
		t.Fatalf("keys = %q", got)
	}
}

func TestSupervisorKeysRefuseAfterANewerPress(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	rt.set(func(rt *fakeRuntime) { rt.status, rt.revision, rt.screen = "blocked", 7, promptEarly })
	h.ok("supervisor", "keys", "--revision", "7", "--after", "0", "1")
	events, err := openStore(t, h).EventsAfter(context.Background(), 0, []string{"supervisor.keys"}, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("keys events = %+v, %v", events, err)
	}
	rt.set(func(rt *fakeRuntime) { rt.revision, rt.screen = 9, promptLate })
	if _, errOut, code := h.run("supervisor", "keys", "--revision", "7", "--screen", luvus.ScreenDigest(promptEarly), "--after", "0", "2"); code != 3 || !strings.Contains(errOut, "a key was pressed on s1 after this screen was read") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if got := rt.keysSent(); !slices.Equal(got, []string{"1"}) {
		t.Fatalf("keys = %q", got)
	}
	has(t, "fresh", h.ok("supervisor", "keys", "--revision", "9", "--after", strconv.FormatInt(events[0].Seq, 10), "2"), "keys: 2")
}

func TestAFailedSwitchLeavesTheStoppedSupervisorToResume(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	codexCache(t, h)
	startClaudeSupervisor(h)
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	if err := os.WriteFile(filepath.Join(h.vars["PATH"], "codex"), []byte("#!/nonexistent/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := h.run("supervisor", "switch", "--harness", "codex", "--model", "gpt-6-luna", "--effort", "low"); code == 0 || !strings.Contains(errOut, "s1 stopped, continue it with") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	has(t, "resume", h.ok("supervisor", "resume"), "supervisor: s3", "harness: claude")
	has(t, "show", h.ok("supervisor", "show"), "supervisor: s3", "resumes: s1")
}

func fastTrust(t *testing.T) {
	t.Helper()
	wait, confirm := *cli.TrustWait, *cli.TrustConfirm
	*cli.TrustWait, *cli.TrustConfirm = 2*time.Second, time.Second
	t.Cleanup(func() { *cli.TrustWait, *cli.TrustConfirm = wait, confirm })
}

func startAgySupervisor(t *testing.T, h *harness, rt *fakeRuntime) string {
	t.Helper()
	fastTrust(t)
	rt.set(func(rt *fakeRuntime) { rt.screen, rt.afterScreen = trustScreen(h.home), "> \n" })
	return h.ok("supervisor", "start", "--harness", "agy", "--model", "gemini-3.8-flash-low")
}

func TestAnAgySupervisorStartsAndAcceptsItsFleetTrust(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	has(t, "start", startAgySupervisor(t, h, rt), "harness: agy", "trust: accepted")
	argv := rt.lastCreate().Command
	want := []string{filepath.Join(h.vars["PATH"], "agy"), "--model", "gemini-3.8-flash-low", "--dangerously-skip-permissions", "-i"}
	if len(argv) != 6 || !slices.Equal(argv[:5], want) || !strings.HasPrefix(argv[5], "You are supervisor s1 of the Hand fleet ") {
		t.Fatalf("argv = %q", argv)
	}
	if !slices.Equal(rt.keysSent(), []string{"enter"}) {
		t.Fatalf("keys = %q", rt.keysSent())
	}
	events, err := openStore(t, h).EventsAfter(context.Background(), 0, []string{"supervisor.keys", "supervisor.blocked"}, 10)
	if err != nil || len(events) != 1 || events[0].Kind != "supervisor.keys" || events[0].Detail != "s1: enter" {
		t.Fatalf("events = %+v, %v", events, err)
	}
}

func TestAClaudeSupervisorStartsAndAcceptsItsFleetTrust(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	fastTrust(t)
	rt.set(func(rt *fakeRuntime) {
		rt.screen, rt.keyScreens = claudeTrustScreen(h.home), []string{claudeTrustScreenYes(h.home), "> \n"}
	})
	out := startClaudeSupervisor(h)
	has(t, "start", out, "harness: claude", "trust: accepted")
	argv := rt.lastCreate().Command
	if len(argv) != 9 || !strings.HasSuffix(argv[0], "/claude") || !strings.HasPrefix(argv[8], "You are supervisor s1 of the Hand fleet ") {
		t.Fatalf("argv = %q", argv)
	}
	if !slices.Equal(rt.keysSent(), []string{"down", "enter"}) {
		t.Fatalf("keys = %q", rt.keysSent())
	}
	events, err := openStore(t, h).EventsAfter(context.Background(), 0, []string{"supervisor.keys", "supervisor.blocked"}, 10)
	if err != nil || len(events) != 1 || events[0].Kind != "supervisor.keys" || events[0].Detail != "s1: down enter" {
		t.Fatalf("events = %+v, %v", events, err)
	}
}

func TestAClaudeSupervisorWithCursorOnYesPressesOnlyEnter(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	fastTrust(t)
	rt.set(func(rt *fakeRuntime) { rt.screen, rt.afterScreen = claudeTrustScreenYes(h.home), "> \n" })
	has(t, "start", startClaudeSupervisor(h), "trust: accepted")
	if !slices.Equal(rt.keysSent(), []string{"enter"}) {
		t.Fatalf("keys = %q", rt.keysSent())
	}
}

func TestAnAgySupervisorFindsAndResumesItsConversation(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startAgySupervisor(t, h, rt)
	argv := rt.lastCreate().Command
	dir := filepath.Join(h.vars["HOME"], ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	agytest.Conversation(t, dir, "c1", h.home, 18001, 256000, agytest.Step{Type: 14, Text: argv[len(argv)-1], At: time.Now()})
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	has(t, "send", h.ok("supervisor", "send", "--text", "hello"), "delivered: yes")
	has(t, "show", h.ok("supervisor", "show"), "session: c1")
	h.ok("supervisor", "stop")
	has(t, "resume", h.ok("supervisor", "resume"), "supervisor: s2", "session: c1")
	if got := rt.lastCreate().Command; !slices.Equal(got[1:], []string{"--model", "gemini-3.8-flash-low", "--dangerously-skip-permissions", "--conversation", "c1"}) {
		t.Fatalf("resume argv = %q", got)
	}
}

func TestSwitchingFromAgyToClaudeAndBack(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startAgySupervisor(t, h, rt)
	dir := agyConversations(t, h)
	first := rt.lastCreate().Command
	agytest.Conversation(t, dir, "c1", h.home, 0, 0, agytest.Step{Type: 14, Text: first[len(first)-1], At: time.Now()})
	rt.set(func(rt *fakeRuntime) { rt.status, rt.screen = "idle", "" })
	h.ok("supervisor", "send", "--text", "hello")
	has(t, "s1", h.ok("supervisor", "show"), "session: c1")
	has(t, "to claude", h.ok("supervisor", "switch", "--harness", "claude", "--model", "sonnet", "--effort", "low"), "supervisor: s2", "harness: claude")
	if argv := rt.lastCreate().Command; !slices.Contains(argv, "--session-id") || !strings.Contains(argv[len(argv)-1], "You replace s1, which ran on agy;") {
		t.Fatalf("claude argv = %q", argv)
	}
	has(t, "back to agy", h.ok("supervisor", "switch", "--harness", "agy", "--model", "gemini-3.8-flash-low"), "supervisor: s3", "harness: agy", "trust: not asked")
	third := rt.lastCreate().Command
	if !slices.Contains(third, "-i") || !strings.Contains(third[len(third)-1], "You replace s2, which ran on claude;") {
		t.Fatalf("agy argv = %q", third)
	}
	later := agytest.Conversation(t, dir, "c3", h.home, 0, 0, agytest.Step{Type: 14, Text: third[len(third)-1], At: time.Now()})
	if err := os.Chtimes(later, time.Now().Add(time.Second), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	h.ok("supervisor", "send", "--text", "again")
	has(t, "s3", h.ok("supervisor", "show"), "supervisor: s3", "session: c3")
	h.ok("supervisor", "stop")
	h.ok("supervisor", "resume")
	if got := rt.lastCreate().Command; !slices.Equal(got[len(got)-2:], []string{"--conversation", "c3"}) {
		t.Fatalf("resume argv = %q", got)
	}
}

func agyConversations(t *testing.T, h *harness) string {
	t.Helper()
	dir := filepath.Join(h.vars["HOME"], ".gemini", "antigravity-cli", "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAgyDiscoveryIgnoresAnotherFleetsConversation(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startAgySupervisor(t, h, rt)
	argv := rt.lastCreate().Command
	prompt := argv[len(argv)-1]
	dir := agyConversations(t, h)
	agytest.Conversation(t, dir, "mine", "", 0, 0, agytest.Step{Type: 14, Text: prompt, At: time.Now()})
	other := strings.Replace(prompt, "of the Hand fleet ", "of the Hand fleet other-", 1)
	theirs := agytest.Conversation(t, dir, "theirs", "", 0, 0, agytest.Step{Type: 14, Text: other, At: time.Now()})
	if err := os.Chtimes(theirs, time.Now().Add(time.Minute), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	h.ok("supervisor", "send", "--text", "hello")
	has(t, "show", h.ok("supervisor", "show"), "session: mine")
}

func TestDeliveryWaitsWhileAgyAsksForTrust(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	fastTrust(t)
	rt.set(func(rt *fakeRuntime) { rt.screen = trustScreen("/elsewhere") })
	has(t, "start", h.ok("supervisor", "start", "--harness", "agy", "--model", "gemini-3.8-flash-low"), "trust: not pressed")
	argv := rt.lastCreate().Command
	agytest.Conversation(t, agyConversations(t, h), "c1", "", 0, 0, agytest.Step{Type: 14, Text: argv[len(argv)-1], At: time.Now()})
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	has(t, "send", h.ok("supervisor", "send", "--text", "hello"), "delivered: no", "agy is asking to trust a folder")
	if got := rt.prompts(); len(got) != 0 {
		t.Fatalf("typed into the trust screen: %q", got)
	}
}

func TestDeliveryWaitsWhileClaudeAsksForTrust(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	fastTrust(t)
	rt.set(func(rt *fakeRuntime) { rt.screen = claudeTrustScreen("/elsewhere") })
	has(t, "start", startClaudeSupervisor(h), "trust: not pressed")
	rt.set(func(rt *fakeRuntime) { rt.status = "idle" })
	has(t, "send", h.ok("supervisor", "send", "--text", "hello"), "delivered: no", "claude is asking to trust a folder")
	if got := rt.prompts(); len(got) != 0 {
		t.Fatalf("typed into the trust screen: %q", got)
	}
}

func TestAnAgyStartWithoutATrustScreenDoesNotWait(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	fastTrust(t)
	*cli.TrustWait = 10 * time.Second
	rt.set(func(rt *fakeRuntime) { rt.status = "working" })
	began := time.Now()
	has(t, "start", h.ok("supervisor", "start", "--harness", "agy", "--model", "gemini-3.8-flash-low"), "trust: not asked")
	if took := time.Since(began); took > 3*time.Second {
		t.Fatalf("start waited %s for a trust screen that never came", took)
	}
}

func firstSession(t *testing.T, h *harness) string {
	t.Helper()
	sups, err := openStore(t, h).Supervisors(context.Background())
	if err != nil || len(sups) == 0 {
		t.Fatalf("supervisors = %+v, %v", sups, err)
	}
	for _, s := range sups {
		if s.ID == 1 {
			return s.Session
		}
	}
	t.Fatal("no s1")
	return ""
}

func TestStoppingKeepsAnUndiscoveredSession(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startAgySupervisor(t, h, rt)
	argv := rt.lastCreate().Command
	agytest.Conversation(t, agyConversations(t, h), "c1", h.home, 0, 0, agytest.Step{Type: 14, Text: argv[len(argv)-1], At: time.Now()})
	h.ok("supervisor", "stop")
	if got := firstSession(t, h); got != "c1" {
		t.Fatalf("s1 session = %q, want c1", got)
	}
}

func TestSwitchingHarnessKeepsAnUndiscoveredSession(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startAgySupervisor(t, h, rt)
	argv := rt.lastCreate().Command
	agytest.Conversation(t, agyConversations(t, h), "c1", h.home, 0, 0, agytest.Step{Type: 14, Text: argv[len(argv)-1], At: time.Now()})
	rt.set(func(rt *fakeRuntime) { rt.status, rt.screen = "idle", "" })
	h.ok("supervisor", "switch", "--harness", "claude", "--model", "sonnet", "--effort", "low")
	if got := firstSession(t, h); got != "c1" {
		t.Fatalf("s1 session = %q, want c1", got)
	}
}

func TestADeliveryCancelledMidPromptIsStillRecorded(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	fx := &attemptFixture{h: h, rt: rt}
	pane := supervisorPane(t, startClaudeSupervisor(h))
	rt.set(func(rt *fakeRuntime) { rt.status, rt.hint = "blocked", "Trust this folder?" })
	has(t, "send", h.ok("supervisor", "send", "--text", "hello"), "delivered: no")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.set(func(rt *fakeRuntime) { rt.onPrompt = cancel })
	done := make(chan struct{})
	go func() {
		h.runCtx(ctx, "watch", "--every", "1h")
		close(done)
	}()
	eventually(t, func() bool { return rt.srv.Subscribers() == 1 })
	publishStatus(fx, pane, "idle")
	<-done
	rt.set(func(rt *fakeRuntime) { rt.onPrompt = nil })
	has(t, "show", h.ok("supervisor", "show"), "pending: 0")
	h.ok("supervisor", "send", "--text", "again")
	if got := rt.prompts(); !slices.Equal(got, []string{"hello", "again"}) {
		t.Fatalf("prompts = %q, want hello once", got)
	}
}

func TestSupervisorStopEndsTheRowWhenItsTerminalWillNotClose(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	rt.set(func(rt *fakeRuntime) { rt.closeFail = "backend_error" })
	out, errOut, code := h.run("supervisor", "stop")
	if code != 0 || !strings.Contains(out, "status: stopped") || !strings.Contains(out, "did not close") {
		t.Fatalf("stop: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	has(t, "show", h.ok("supervisor", "show"), "status: stopped")
}

func TestSwitchingHarnessSaysWhenTheOldTerminalWillNotClose(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	codexCache(t, h)
	startClaudeSupervisor(h)
	rt.set(func(rt *fakeRuntime) { rt.status, rt.closeFail = "idle", "backend_error" })
	out, errOut, code := h.run("supervisor", "switch", "--harness", "codex", "--model", "gpt-6-luna", "--effort", "low")
	if code != 0 || !strings.Contains(out, "supervisor: s2") || !strings.Contains(errOut, "did not close") {
		t.Fatalf("switch: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
}

func TestAWatcherStoppedMidQueueLeavesTheRestPending(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	fx := &attemptFixture{h: h, rt: rt}
	pane := supervisorPane(t, startClaudeSupervisor(h))
	rt.set(func(rt *fakeRuntime) { rt.status, rt.hint = "blocked", "Trust this folder?" })
	h.ok("supervisor", "send", "--text", "one")
	h.ok("supervisor", "send", "--text", "two")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.set(func(rt *fakeRuntime) { rt.onPrompt = cancel })
	done := make(chan struct{})
	go func() {
		h.runCtx(ctx, "watch", "--every", "1h")
		close(done)
	}()
	eventually(t, func() bool { return rt.srv.Subscribers() == 1 })
	publishStatus(fx, pane, "idle")
	<-done
	rt.set(func(rt *fakeRuntime) { rt.onPrompt = nil })
	if got := rt.prompts(); !slices.Equal(got, []string{"one"}) {
		t.Fatalf("prompts after the stop = %q, want only the one in flight", got)
	}
	has(t, "show", h.ok("supervisor", "show"), "pending: 1")
}
