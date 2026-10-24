package e2e_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/e2e/mockllm"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

type sandbox struct {
	t                                 *testing.T
	root, home, codexHome, shm, luvus string
	fleet, id                         string
	mock                              *mockllm.Server
	extraEnv                          []string
}

type result struct {
	out, errOut string
	code        int
}

func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	enabled(t)
	root, err := os.MkdirTemp("", "he2e")
	if err != nil {
		t.Fatal(err)
	}
	sb := &sandbox{t: t, root: root, home: filepath.Join(root, "home"), codexHome: filepath.Join(root, "codex"), shm: filepath.Join(root, "shm"), luvus: filepath.Join(root, "luvus"), fleet: filepath.Join(root, "fleet")}
	for _, d := range []string{sb.home, sb.codexHome, sb.fleet} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitconfig := "[user]\n\tname = e2e\n\temail = e2e@example.invalid\n[commit]\n\tgpgsign = false\n[init]\n\tdefaultBranch = main\n"
	sb.write(filepath.Join(sb.home, ".gitconfig"), gitconfig)
	t.Cleanup(sb.cleanup)
	return sb
}

func (sb *sandbox) write(path, content string) {
	sb.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		sb.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		sb.t.Fatal(err)
	}
}

var (
	droppedNames    = []string{"HOME", "USERPROFILE", "XDG_RUNTIME_DIR"}
	droppedPrefixes = []string{"CLAUDE", "ANTHROPIC_", "OPENAI_", "CODEX_", "HAND_", "LUVUS_", "SECONDHAND_", "GIT_"}
)

func (sb *sandbox) environ() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		name = strings.ToUpper(name)
		if !slices.Contains(droppedNames, name) && !slices.ContainsFunc(droppedPrefixes, func(p string) bool { return strings.HasPrefix(name, p) }) {
			env = append(env, kv)
		}
	}
	env = append(env, "HOME="+sb.home, "CODEX_HOME="+sb.codexHome, "SECONDHAND_HOME="+sb.shm, "LUVUS_HOME="+sb.luvus, "GIT_CONFIG_GLOBAL="+filepath.Join(sb.home, ".gitconfig"))
	if runtime.GOOS == "windows" {
		env = append(env, "USERPROFILE="+sb.home)
	}
	return append(env, sb.extraEnv...)
}

func (sb *sandbox) hand(args ...string) result {
	sb.t.Helper()
	return sb.handIn(sb.fleet, 3*time.Minute, args...)
}

func (sb *sandbox) handIn(dir string, limit time.Duration, args ...string) result {
	sb.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, handBin, args...)
	cmd.Dir, cmd.Env, cmd.WaitDelay = dir, sb.environ(), 3*time.Second
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return result{out.String(), errOut.String(), 0}
	case errors.As(err, &exit):
		return result{out.String(), errOut.String(), exit.ExitCode()}
	}
	sb.t.Fatalf("hand %q: %v\n%s", args, err, errOut.String())
	return result{}
}

func (sb *sandbox) ok(args ...string) string {
	sb.t.Helper()
	r := sb.hand(args...)
	if r.code != 0 {
		sb.t.Fatalf("hand %q: exit %d\nstdout: %s\nstderr: %s", args, r.code, r.out, r.errOut)
	}
	if r.errOut != "" {
		sb.t.Logf("hand %q wrote to stderr: %s", args, r.errOut)
	}
	return r.out
}

func field(out, name string) string {
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(l, name+": "); ok {
			if u, err := strconv.Unquote(v); err == nil {
				return u
			}
			return v
		}
	}
	return ""
}

func (sb *sandbox) init() {
	sb.t.Helper()
	out := sb.ok("init")
	if sb.id = field(out, "id"); sb.id == "" {
		sb.t.Fatalf("init printed no id: %s", out)
	}
}

func (sb *sandbox) store() *state.Store {
	sb.t.Helper()
	home, err := filepath.EvalSymlinks(sb.fleet)
	if err != nil {
		sb.t.Fatal(err)
	}
	st, err := state.Open(filepath.Join(home, "hand.db"), time.Now)
	if err != nil {
		sb.t.Fatal(err)
	}
	return st
}

func (sb *sandbox) events() []state.Event {
	sb.t.Helper()
	st := sb.store()
	defer st.Close()
	events, err := st.EventsAfter(context.Background(), 0, nil, 10000)
	if err != nil {
		sb.t.Fatal(err)
	}
	return events
}

func (sb *sandbox) reports() []state.Report {
	sb.t.Helper()
	st := sb.store()
	defer st.Close()
	reports, err := st.Reports(context.Background(), state.ReportFilter{Oldest: true}, 100)
	if err != nil {
		sb.t.Fatal(err)
	}
	return reports
}

func (sb *sandbox) attempt(id int64) state.Attempt {
	sb.t.Helper()
	st := sb.store()
	defer st.Close()
	a, err := st.Attempt(context.Background(), id)
	if err != nil {
		sb.t.Fatal(err)
	}
	return a
}

func (sb *sandbox) startMock(steps ...mockllm.Step) *mockllm.Server {
	sb.t.Helper()
	m, err := mockllm.Start(steps...)
	if err != nil {
		sb.t.Fatal(err)
	}
	sb.mock = m
	return m
}

func (sb *sandbox) repo(name string) string {
	sb.t.Helper()
	dir := filepath.Join(sb.root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		sb.t.Fatal(err)
	}
	sb.write(filepath.Join(dir, "README.md"), "hello\n")
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "README.md"}, {"commit", "-q", "-m", "init"}} {
		sb.git(dir, args...)
	}
	return dir
}

func (sb *sandbox) git(dir string, args ...string) string {
	sb.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = sb.environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		sb.t.Fatalf("git %q: %v: %s", args, err, out)
	}
	return string(out)
}

func (sb *sandbox) until(what string, limit time.Duration, f func() bool) {
	sb.t.Helper()
	deadline := time.Now().Add(limit)
	for !f() {
		if time.Now().After(deadline) {
			sb.t.Fatalf("timed out after %s waiting for %s", limit, what)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (sb *sandbox) mainRequests() []mockllm.Request {
	var out []mockllm.Request
	for _, r := range sb.mock.Requests() {
		if r.Turn >= 0 {
			out = append(out, r)
		}
	}
	return out
}

func (sb *sandbox) turn(n int) (mockllm.Request, bool) {
	for _, r := range sb.mainRequests() {
		if r.Turn == n {
			return r, true
		}
	}
	return mockllm.Request{}, false
}

var refPattern = regexp.MustCompile(`^[a-z]\d+$`)

func (sb *sandbox) luvusCLI(args ...string) string {
	pin, ok, err := luvus.LoadPin(sb.shm)
	if err != nil || !ok {
		return "no pinned luvus\n"
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, pin.Path, append([]string{"--session", "secondhand-" + sb.id}, args...)...)
	cmd.Env, cmd.WaitDelay = luvus.Scrub(sb.environ()), 3*time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out) + err.Error() + "\n"
	}
	return string(out)
}

func (sb *sandbox) dump() {
	art := os.Getenv("HAND_E2E_ARTIFACTS")
	if art == "" || sb.id == "" {
		return
	}
	dir := filepath.Join(art, strings.NewReplacer("/", "_", " ", "_").Replace(sb.t.Name()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	var b strings.Builder
	run := func(args ...string) string {
		r := sb.handIn(sb.fleet, time.Minute, args...)
		b.WriteString("$ hand " + strings.Join(args, " ") + "\n" + r.out + r.errOut + "\n")
		return r.out
	}
	panes := map[string]bool{}
	pane := func(out string) {
		if p := field(out, "pane"); p != "" {
			panes[p] = true
		}
	}
	run("orient")
	list := run("attempt", "list")
	super := run("supervisor", "show")
	pane(super)
	run("luvus", "show")
	for _, line := range strings.Split(list, "\n") {
		if ref, _, _ := strings.Cut(strings.TrimSpace(line), ","); refPattern.MatchString(ref) {
			pane(run("attempt", "show", ref))
			run("attempt", "read", ref)
		}
	}
	for _, log := range []string{filepath.Join(sb.fleet, "watch.log"), filepath.Join(sb.shm, "board.log")} {
		if data, err := os.ReadFile(log); err == nil {
			b.WriteString("$ cat " + filepath.Base(log) + "\n" + string(data) + "\n")
		}
	}
	b.WriteString("events:\n")
	for _, e := range sb.events() {
		b.WriteString(fmt.Sprintf("  %d %s %s %q\n", e.Seq, e.At, e.Kind, e.Detail))
	}
	for p := range panes {
		for _, args := range [][]string{{"agent", "explain", p}, {"pane", "processes", p}, {"pane", "read", p}} {
			out := sb.luvusCLI(args...)
			b.WriteString("$ luvus " + strings.Join(args, " ") + "\n" + out + "\n")
			if args[0] == "pane" && args[1] == "read" && strings.Contains(out, "No, exit") {
				b.WriteString("$ luvus agent keys " + p + " down\n" + sb.luvusCLI("agent", "keys", p, "down") + "\n")
				time.Sleep(2 * time.Second)
				b.WriteString("$ luvus pane read " + p + " (after the key)\n" + sb.luvusCLI("pane", "read", p) + "\n")
			}
		}
	}
	if field(super, "status") == "running" {
		run("supervisor", "force")
		b.WriteString("$ luvus agent prompt " + field(super, "pane") + " (a probe)\n" + sb.luvusCLI("agent", "prompt", field(super, "pane"), "e2e probe") + "\n")
		time.Sleep(3 * time.Second)
		b.WriteString("$ luvus pane read " + field(super, "pane") + " (after supervisor force)\n" + sb.luvusCLI("pane", "read", field(super, "pane")) + "\n")
	}
	_ = os.WriteFile(filepath.Join(dir, "hand.txt"), []byte(b.String()), 0o644)
}

func (sb *sandbox) cleanup() {
	if sb.t.Failed() {
		sb.dump()
	}
	if art := os.Getenv("HAND_E2E_ARTIFACTS"); art != "" && sb.mock != nil {
		dir := filepath.Join(art, strings.NewReplacer("/", "_", " ", "_").Replace(sb.t.Name()))
		if os.MkdirAll(dir, 0o755) == nil {
			_ = os.WriteFile(filepath.Join(dir, "mock.log"), sb.mock.Dump(), 0o644)
		}
	}
	if sb.mock != nil {
		_ = sb.mock.Close()
	}
	if sb.id != "" {
		sb.handIn(sb.fleet, time.Minute, "watch", "stop")
		if pin, ok, err := luvus.LoadPin(sb.shm); err == nil && ok {
			stop := exec.Command(pin.Path, "--session", "secondhand-"+sb.id, "server", "stop")
			stop.Env = luvus.Scrub(sb.environ())
			_ = stop.Run()
		}
	}
	for range 10 {
		if os.RemoveAll(sb.root) == nil {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}
