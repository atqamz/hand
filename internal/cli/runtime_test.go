package cli_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
	"github.com/atqamz/hand/internal/proc"
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
	srv         *fakeuhp.Server
	mu          sync.Mutex
	terms       []*fakeTerm
	creates     []createCall
	status      string
	hint        string
	ready       bool
	revision    int64
	marker      string
	sent        []string
	keyed       []string
	screen      string
	screens     []string
	afterScreen string
	keyScreens  []string
	afterStall  time.Duration
	stall       time.Duration
	readFail    string
	keysFail    string
	afterKeys   string
	explainFail string
	onPrompt    func()
	closeFail   string
	closeDelay  time.Duration
	sessions    map[string]string
	blankPane   bool
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
	proc.NewGroup(cmd)
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
	pane := term.pane
	if rt.blankPane {
		pane = ""
	}
	return map[string]any{
		"server_generation": rt.srv.Generation(),
		"terminal_id":       term.id,
		"pane_id":           pane,
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
		killGroup(term.cmd, false)
	}
	<-term.done
	rt.mu.Lock()
	fail := rt.closeFail
	if fail == "" {
		term.closed = true
	}
	delay := rt.closeDelay
	rt.mu.Unlock()
	if fail != "" {
		return nil, fakeuhp.Fail{Code: fail, Message: "the backend could not close the terminal"}
	}
	rt.srv.Publish("pane.closed", map[string]any{"pane": term.pane})
	time.Sleep(delay)
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

func (rt *fakeRuntime) explain(params json.RawMessage) (any, error) {
	var p struct {
		Pane string `json:"pane"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.explainFail != "" {
		return nil, fakeuhp.Fail{Code: rt.explainFail, Message: "explain failed"}
	}
	out := map[string]any{"pane": p.Pane, "agent": "claude", "status": rt.status, "state_evidence": map[string]any{"blocked_hint": rt.hint}}
	if id, ok := rt.sessions[p.Pane]; ok {
		out["session"] = map[string]any{"agent": "claude", "id": id}
	}
	return out, nil
}

func (rt *fakeRuntime) setSession(terminalID, session string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.sessions == nil {
		rt.sessions = map[string]string{}
	}
	for _, term := range rt.terms {
		if term.id == terminalID {
			rt.sessions[term.pane] = session
		}
	}
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
	if rt.onPrompt != nil {
		rt.onPrompt()
	}
	return map[string]any{"submitted": true}, nil
}

func (rt *fakeRuntime) read(json.RawMessage) (any, error) {
	rt.mu.Lock()
	stall := rt.stall
	rt.mu.Unlock()
	time.Sleep(stall)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.readFail != "" {
		return nil, fakeuhp.Fail{Code: rt.readFail, Message: "terminal is gone"}
	}
	text := "Do you want to proceed?\n❯ 1. Yes"
	if rt.screen != "" {
		text = rt.screen
	}
	if len(rt.screens) > 0 {
		text, rt.screens = rt.screens[0], rt.screens[1:]
	}
	return map[string]any{"text": text, "content_revision": rt.revision, "terminal_id": rt.terms[len(rt.terms)-1].id}, nil
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
	if rt.keysFail != "" {
		return nil, fakeuhp.Fail{Code: rt.keysFail, Message: "keys failed"}
	}
	if p.Revision != rt.revision {
		return nil, fakeuhp.Fail{Code: "content_revision_conflict", Message: "expected content_revision=" + strconv.FormatInt(p.Revision, 10)}
	}
	rt.keyed = append(rt.keyed, p.Keys...)
	if rt.afterKeys != "" {
		rt.status = rt.afterKeys
	}
	if rt.afterScreen != "" {
		rt.screen = rt.afterScreen
	}
	if len(rt.keyScreens) > 0 {
		rt.screen, rt.keyScreens = rt.keyScreens[0], rt.keyScreens[1:]
	}
	rt.stall = rt.afterStall
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
			killGroup(term.cmd, true)
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
		{"config", "maintenance.autoDetach", "false"},
		{"config", "gc.autoDetach", "false"},
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
	for _, name := range []string{"claude", "codex", "opencode"} {
		fakebin.Install(t, dir, name, "fake", map[string]string{"sleep": "300s"})
	}
	fakebin.Install(t, dir, "agy", "fake", map[string]string{"on models": "gemini-3.8-flash-low\tGemini 3.8 Flash (Low)\n", "sleep": "300s"})
	return dir
}

func newAttemptFixture(t *testing.T) *attemptFixture {
	t.Helper()
	fx := &attemptFixture{h: newHarness(t), repo: gitRepo(t)}
	fx.h.now = time.Now()
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
