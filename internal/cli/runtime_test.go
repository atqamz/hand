package cli_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"testing"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
)

type createCall struct {
	CWD     string   `json:"cwd"`
	Label   string   `json:"label"`
	Command []string `json:"command"`
}

type fakeTerm struct {
	id, pane, cwd, label string
	cmd                  *exec.Cmd
	marker               string
	done                 chan struct{}
	closed               bool
}

type fakeRuntime struct {
	srv      *fakeuhp.Server
	mu       sync.Mutex
	terms    []*fakeTerm
	creates  []createCall
	status   string
	hint     string
	ready    bool
	revision int64
	marker   string
	sent     []string
	keyed    []string
}

func startRuntime(t *testing.T, socket string) *fakeRuntime {
	t.Helper()
	rt := &fakeRuntime{srv: fakeuhp.Start(t, socket), status: "working", ready: true}
	rt.srv.Handle("terminal.backend.create", rt.create)
	rt.srv.Handle("terminal.backend.validate", rt.validate)
	rt.srv.Handle("terminal.backend.close", rt.close)
	rt.srv.Handle("terminal.backend.inventory", rt.inventory)
	rt.srv.Handle("agent.explain", rt.explain)
	rt.srv.Handle("agent.prompt", rt.prompt)
	rt.srv.Handle("agent.read", rt.read)
	rt.srv.Handle("agent.keys", rt.keys)
	t.Cleanup(rt.exitAll)
	return rt
}

func (rt *fakeRuntime) spawn(cwd, label string, argv []string) (*fakeTerm, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = cwd
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	marker, err := luvus.ProcStartMarker(cmd.Process.Pid)
	if err != nil {
		return nil, err
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.marker != "" {
		marker = rt.marker
	}
	term := &fakeTerm{id: "term-" + strconv.Itoa(len(rt.terms)+1), pane: strconv.Itoa(len(rt.terms) + 2), cwd: cwd, label: label, cmd: cmd, marker: marker, done: make(chan struct{})}
	rt.terms = append(rt.terms, term)
	go func() { _ = cmd.Wait(); close(term.done) }()
	return term, nil
}

func (rt *fakeRuntime) wire(term *fakeTerm) map[string]any {
	return map[string]any{
		"server_generation": rt.srv.Generation(),
		"terminal_id":       term.id,
		"pane_id":           term.pane,
		"cwd":               term.cwd,
		"label":             term.label,
		"root_process":      map[string]any{"pid": term.cmd.Process.Pid, "start_marker": term.marker},
	}
}

func exited(term *fakeTerm) bool {
	select {
	case <-term.done:
		return true
	default:
		return false
	}
}

func (rt *fakeRuntime) create(params json.RawMessage) (any, error) {
	var call createCall
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, err
	}
	term, err := rt.spawn(call.CWD, call.Label, call.Command)
	if err != nil {
		return nil, fakeuhp.Fail{Code: "create_failed", Message: err.Error()}
	}
	rt.mu.Lock()
	rt.creates = append(rt.creates, call)
	rt.mu.Unlock()
	return rt.wire(term), nil
}

func (rt *fakeRuntime) locate(params json.RawMessage) (*fakeTerm, error) {
	var loc struct {
		ServerGeneration string `json:"server_generation"`
		TerminalID       string `json:"terminal_id"`
	}
	if err := json.Unmarshal(params, &loc); err != nil {
		return nil, err
	}
	if loc.ServerGeneration != rt.srv.Generation() {
		return nil, fakeuhp.Fail{Code: "stale_server", Message: "server generation changed"}
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, term := range rt.terms {
		if term.id == loc.TerminalID && !term.closed {
			return term, nil
		}
	}
	return nil, fakeuhp.Fail{Code: "stale_terminal", Message: "terminal is gone"}
}

func (rt *fakeRuntime) validate(params json.RawMessage) (any, error) {
	term, err := rt.locate(params)
	if err != nil {
		return nil, err
	}
	if exited(term) {
		return map[string]any{"state": "gone"}, nil
	}
	return map[string]any{"state": "alive"}, nil
}

func (rt *fakeRuntime) close(params json.RawMessage) (any, error) {
	term, err := rt.locate(params)
	if err != nil {
		return nil, err
	}
	if !exited(term) {
		_ = syscall.Kill(-term.cmd.Process.Pid, syscall.SIGHUP)
	}
	<-term.done
	rt.mu.Lock()
	term.closed = true
	rt.mu.Unlock()
	return map[string]any{"state": "succeeded"}, nil
}

func (rt *fakeRuntime) inventory(json.RawMessage) (any, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	terms := []any{}
	for _, term := range rt.terms {
		if !term.closed && !exited(term) {
			terms = append(terms, rt.wire(term))
		}
	}
	return map[string]any{"server_generation": rt.srv.Generation(), "terminals": terms}, nil
}

func (rt *fakeRuntime) explain(json.RawMessage) (any, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return map[string]any{"pane": "2", "agent": "claude", "status": rt.status, "state_evidence": map[string]any{"blocked_hint": rt.hint}}, nil
}

func (rt *fakeRuntime) prompt(params json.RawMessage) (any, error) {
	var p struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if !rt.ready {
		return nil, fakeuhp.Fail{Code: "agent_not_ready", Message: "no prompt input was queued"}
	}
	rt.sent = append(rt.sent, p.Text)
	return map[string]any{"submitted": true}, nil
}

func (rt *fakeRuntime) read(json.RawMessage) (any, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return map[string]any{"text": "Do you want to proceed?\n❯ 1. Yes", "content_revision": rt.revision, "terminal_id": rt.terms[len(rt.terms)-1].id}, nil
}

func (rt *fakeRuntime) keys(params json.RawMessage) (any, error) {
	var p struct {
		Keys     []string `json:"keys"`
		Revision int64    `json:"if_content_revision"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if p.Revision != rt.revision {
		return nil, fakeuhp.Fail{Code: "content_revision_conflict", Message: "expected content_revision=" + strconv.FormatInt(p.Revision, 10)}
	}
	rt.keyed = append(rt.keyed, p.Keys...)
	return map[string]any{"type": "ok"}, nil
}

func (rt *fakeRuntime) set(fn func(*fakeRuntime)) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	fn(rt)
}

func (rt *fakeRuntime) lastCreate() createCall {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.creates[len(rt.creates)-1]
}

func (rt *fakeRuntime) lastPID() int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.terms[len(rt.terms)-1].cmd.Process.Pid
}

func (rt *fakeRuntime) prompts() []string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return slices.Clone(rt.sent)
}

func (rt *fakeRuntime) keysSent() []string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return slices.Clone(rt.keyed)
}

func (rt *fakeRuntime) exitAll() {
	rt.mu.Lock()
	terms := slices.Clone(rt.terms)
	rt.mu.Unlock()
	for _, term := range terms {
		if !exited(term) {
			_ = syscall.Kill(-term.cmd.Process.Pid, syscall.SIGKILL)
		}
		<-term.done
	}
}

func (rt *fakeRuntime) addShell(t *testing.T, cwd, label string) string {
	t.Helper()
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	term, err := rt.spawn(cwd, label, []string{sleep, "300"})
	if err != nil {
		t.Fatal(err)
	}
	return term.id
}

func (rt *fakeRuntime) isClosed(id string) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, term := range rt.terms {
		if term.id == id {
			return term.closed
		}
	}
	return false
}

type attemptFixture struct {
	h     *harness
	rt    *fakeRuntime
	repo  string
	brief string
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "root"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %q: %v: %s", args, err, out)
		}
	}
	return dir
}

func fakeBin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexec sleep 300\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func newAttemptFixture(t *testing.T) *attemptFixture {
	t.Helper()
	fx := &attemptFixture{h: newHarness(t), repo: gitRepo(t)}
	fx.rt = startRuntime(t, filepath.Join(t.TempDir(), "uhp.sock"))
	fx.h.vars["HAND_LUVUS_SOCKET"] = fx.rt.srv.Socket
	fx.h.vars["PATH"] = fakeBin(t)
	fx.h.vars["HOME"] = t.TempDir()
	fx.brief = filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(fx.brief, []byte("Fix the login bug, commit, then stop."), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.h.ok("init")
	fx.h.ok("project", "add", "app", fx.repo)
	fx.h.ok("task", "add", "app", "Fix login")
	fx.h.ok("task", "start", "t1")
	return fx
}

func (fx *attemptFixture) start() string {
	fx.h.t.Helper()
	return fx.h.ok("attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--prompt-file", fx.brief, "t1")
}
