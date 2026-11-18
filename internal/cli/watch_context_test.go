package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

type contextFixture struct {
	t         *testing.T
	st        *state.Store
	home      string
	claudeDir string
	vars      map[string]string
	now       time.Time
	task      int
}

func newContextFixture(t *testing.T) *contextFixture {
	t.Helper()
	ctx := context.Background()
	f := &contextFixture{t: t, home: t.TempDir(), claudeDir: t.TempDir(), now: time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)}
	f.vars = map[string]string{"CLAUDE_CONFIG_DIR": f.claudeDir, "HOME": t.TempDir(), "COPILOT_HOME": t.TempDir()}
	st, err := state.Open(filepath.Join(f.home, "hand.db"), func() time.Time { return f.now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f.st = st
	if _, err := st.CreateFleet(ctx, "f"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddProject(ctx, "hand", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *contextFixture) watcher() *watcher {
	r := &runner{env: Env{Getenv: func(k string) string { return f.vars[k] }, Now: func() time.Time { return f.now }}, home: f.home, root: f.t.TempDir(), fleet: state.Fleet{ID: "f"}}
	return &watcher{r: r, st: f.st}
}

func (f *contextFixture) attempt(name string, argv []string, worktree string) state.Attempt {
	f.t.Helper()
	ctx := context.Background()
	f.task++
	task, err := f.st.AddTask(ctx, "hand", "task "+strconv.Itoa(f.task), "")
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.st.Transition(ctx, task.ID, state.StatusActive); err != nil {
		f.t.Fatal(err)
	}
	a, err := f.st.AddAttempt(ctx, state.AttemptSpec{TaskID: task.ID, Harness: name, Model: "m", Effort: "low", Argv: argv}, worktree)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t" + strconv.Itoa(f.task), PaneID: "1", PID: 1, StartMarker: "m"}); err != nil {
		f.t.Fatal(err)
	}
	return a
}

func (f *contextFixture) claude(tokens int) (state.Attempt, string) {
	f.t.Helper()
	session := harness.NewSessionID()
	a := f.attempt("claude", []string{"claude", "--session-id", session, "brief"}, f.t.TempDir())
	f.setClaude(session, tokens)
	return a, session
}

func (f *contextFixture) setClaude(session string, tokens int) {
	f.t.Helper()
	path := filepath.Join(f.claudeDir, "projects", "p", session+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	line := `{"type":"assistant","timestamp":"2026-11-18T09:00:01Z","message":{"model":"claude-sonnet-5-5","usage":{"input_tokens":1,"cache_creation_input_tokens":` + strconv.Itoa(tokens-1) + `,"cache_read_input_tokens":0},"content":[]}}` + "\n"
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		f.t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(line); err != nil {
		f.t.Fatal(err)
	}
}

func (f *contextFixture) sweep(w *watcher) {
	f.t.Helper()
	w.sweepModels(context.Background())
	w.pending.Wait()
}

func (f *contextFixture) events(a state.Attempt, kind string) []string {
	f.t.Helper()
	events, err := f.st.RecentEventsOf(context.Background(), []string{kind}, 50)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []string
	prefix := state.AttemptRef(a.ID) + ": "
	for _, e := range events {
		if strings.HasPrefix(e.Detail, prefix) {
			out = append(out, strings.TrimPrefix(e.Detail, prefix))
		}
	}
	return out
}

func sameStrings(got, want []string) bool {
	return fmt.Sprint(got) == fmt.Sprint(want)
}

func TestWatcherRecordsEachContextMarkOncePerWorker(t *testing.T) {
	f := newContextFixture(t)
	a, session := f.claude(120_000)
	w := f.watcher()

	f.sweep(w)
	if got := f.events(a, "attempt.context"); !sameStrings(got, []string{"100000: context 120000"}) {
		t.Fatalf("after 120k: %v", got)
	}
	f.setClaude(session, 155_000)
	f.sweep(w)
	f.sweep(w)
	if got := f.events(a, "attempt.context"); !sameStrings(got, []string{"100000: context 120000", "150000: context 155000"}) {
		t.Fatalf("after 155k: %v", got)
	}
	f.setClaude(session, 80_000)
	f.sweep(w)
	f.setClaude(session, 320_000)
	f.sweep(w)
	if got := f.events(a, "attempt.context"); len(got) != 4 || got[2] != "200000: context 320000" || got[3] != "300000: context 320000" {
		t.Fatalf("after 320k: %v", got)
	}
	f.sweep(w)
	if got := f.events(a, "attempt.context"); len(got) != 4 {
		t.Fatalf("a mark was recorded twice: %v", got)
	}
}

func TestWatcherRecordsContextFromExactlyTheFirstMarkUp(t *testing.T) {
	f := newContextFixture(t)
	a, _ := f.claude(99_999)
	f.sweep(f.watcher())
	if got := f.events(a, "attempt.context"); len(got) != 0 {
		t.Fatalf("events: %v", got)
	}
	exact, _ := f.claude(100_000)
	f.sweep(f.watcher())
	if got := f.events(exact, "attempt.context"); !sameStrings(got, []string{"100000: context 100000"}) {
		t.Fatalf("exactly 100k: %v", got)
	}
}

func TestARestartedWatcherDoesNotRepeatContextMarks(t *testing.T) {
	f := newContextFixture(t)
	a, session := f.claude(160_000)
	f.sweep(f.watcher())
	f.sweep(f.watcher())
	if got := f.events(a, "attempt.context"); len(got) != 2 {
		t.Fatalf("restart repeated marks: %v", got)
	}
	f.setClaude(session, 210_000)
	f.sweep(f.watcher())
	if got := f.events(a, "attempt.context"); len(got) != 3 || got[2] != "200000: context 210000" {
		t.Fatalf("restart missed the new mark: %v", got)
	}
}

func TestAWorkerOnAHarnessWithoutContextRecordsUnknownOnce(t *testing.T) {
	f := newContextFixture(t)
	session := harness.NewSessionID()
	a := f.attempt("copilot", []string{"copilot", "--session-id", session}, f.t.TempDir())
	agy := f.attempt("agy", []string{"agy"}, f.t.TempDir())
	w := f.watcher()
	f.sweep(w)
	f.sweep(w)
	f.sweep(f.watcher())
	for _, c := range []struct {
		a    state.Attempt
		name string
	}{{a, "copilot"}, {agy, "agy"}} {
		if got := f.events(c.a, "attempt.context_unknown"); !sameStrings(got, []string{c.name + " has no context mark"}) {
			t.Fatalf("%s unknown events: %v", c.name, got)
		}
		if got := f.events(c.a, "attempt.context"); len(got) != 0 {
			t.Fatalf("%s context events: %v", c.name, got)
		}
	}
}

func TestALaunchingWorkerRecordsNoContextEvent(t *testing.T) {
	f := newContextFixture(t)
	ctx := context.Background()
	add := func(name string, argv []string) state.Attempt {
		f.task++
		task, err := f.st.AddTask(ctx, "hand", "task "+strconv.Itoa(f.task), "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.st.Transition(ctx, task.ID, state.StatusActive); err != nil {
			t.Fatal(err)
		}
		a, err := f.st.AddAttempt(ctx, state.AttemptSpec{TaskID: task.ID, Harness: name, Model: "m", Effort: "low", Argv: argv}, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	session := harness.NewSessionID()
	claude := add("claude", []string{"claude", "--session-id", session, "brief"})
	copilot := add("copilot", []string{"copilot", "--session-id", harness.NewSessionID()})
	f.setClaude(session, 120_000)
	f.sweep(f.watcher())
	if got := f.events(claude, "attempt.context"); len(got) != 0 {
		t.Fatalf("claude events: %v", got)
	}
	if got := f.events(copilot, "attempt.context_unknown"); len(got) != 0 {
		t.Fatalf("copilot events: %v", got)
	}
}

func TestAReportingHarnessNeverRecordsContextUnknown(t *testing.T) {
	f := newContextFixture(t)
	a, _ := f.claude(10_000)
	f.sweep(f.watcher())
	if got := f.events(a, "attempt.context_unknown"); len(got) != 0 {
		t.Fatalf("events: %v", got)
	}
}

func TestWatcherReadsAnOmpWorkersContextThroughItsSessionLookup(t *testing.T) {
	f := newContextFixture(t)
	marker := "You are worker a1 of the Hand fleet f."
	a := f.attempt("omp", []string{"omp", "--", marker}, t.TempDir())
	f.vars["PI_CODING_AGENT_DIR"] = ".omp-agent"
	worktree := a.Worktree
	agent := filepath.Join(worktree, ".omp-agent")
	stamp := f.now.Add(time.Second).UTC().Format(time.RFC3339Nano)
	body := `{"type":"session","version":3,"id":"s1","timestamp":"` + stamp + `","cwd":` + strconv.Quote(worktree) + `}` + "\n" +
		`{"type":"message","timestamp":"` + stamp + `","message":{"role":"user","content":[{"type":"text","text":` + strconv.Quote(marker) + `}]}}` + "\n" +
		`{"type":"message","timestamp":"` + stamp + `","message":{"role":"assistant","content":[{"type":"text","text":"ok"}],"provider":"acme","model":"m","stopReason":"stop","usage":{"input":10000,"cacheRead":100000,"cacheWrite":45000}}}` + "\n"
	dir := filepath.Join(agent, "sessions", "-fleet")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, f.now.Add(time.Second).UTC().Format("2006-01-02T15-04-05-000Z")+"_s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	w := f.watcher()
	w.transcripts = &transcript.Reader{Paths: transcript.Paths{}}
	w.modelSessions = map[int64]modelSession{}
	w.sources = map[string]modelSource{"omp": &ompSource{transcriptSource{w, "omp"}}}
	f.sweep(w)
	f.sweep(w)
	if got := f.events(a, "attempt.context"); !sameStrings(got, []string{"100000: context 155000", "150000: context 155000"}) {
		t.Fatalf("omp events: %v", got)
	}
}

func TestAnEndedAttemptIsNoLongerTrackedForContext(t *testing.T) {
	f := newContextFixture(t)
	a, session := f.claude(110_000)
	w := f.watcher()
	f.sweep(w)
	if w.contextSeen[a.ID] == nil {
		t.Fatal("a live attempt is not tracked")
	}
	if _, err := f.st.EndAttempt(context.Background(), a.ID, state.AttemptExited, "done"); err != nil {
		t.Fatal(err)
	}
	f.setClaude(session, 160_000)
	f.sweep(w)
	if _, ok := w.contextSeen[a.ID]; ok {
		t.Fatal("an ended attempt is still tracked")
	}
	if got := f.events(a, "attempt.context"); len(got) != 1 {
		t.Fatalf("events after the end: %v", got)
	}
}

func TestContextEventsDoNotChangeTheAttemptsLastSignal(t *testing.T) {
	f := newContextFixture(t)
	ctx := context.Background()
	a, _ := f.claude(120_000)
	if err := f.st.NoteAttempt(ctx, a.ID, "quiet", "turn ended"); err != nil {
		t.Fatal(err)
	}
	f.sweep(f.watcher())
	if got := f.events(a, "attempt.context"); len(got) != 1 {
		t.Fatalf("events: %v", got)
	}
	if kind, err := f.st.LastAttemptEvent(ctx, a.ID); err != nil || kind != "attempt.quiet" {
		t.Fatalf("last event = %q, %v", kind, err)
	}
}

type failsOnce struct {
	context.Context
	asked, failed bool
}

func (c *failsOnce) Done() <-chan struct{} {
	if c.asked {
		return c.Context.Done()
	}
	c.asked = true
	done := make(chan struct{})
	close(done)
	return done
}

func (c *failsOnce) Err() error {
	if c.asked && !c.failed {
		c.failed = true
		return context.Canceled
	}
	return nil
}

func TestAFailedReadOfTheRecordedMarksRecordsNothingAndRepeatsNothing(t *testing.T) {
	f := newContextFixture(t)
	a, _ := f.claude(120_000)
	f.sweep(f.watcher())
	w := f.watcher()
	w.noteContext(&failsOnce{Context: context.Background()}, a, 120_000)
	if got := f.events(a, "attempt.context"); !sameStrings(got, []string{"100000: context 120000"}) {
		t.Fatalf("events after a failed read: %v", got)
	}
	if w.contextSeen[a.ID] != nil {
		t.Fatal("a failed read was remembered as no marks")
	}
	w.noteContext(context.Background(), a, 120_000)
	if got := f.events(a, "attempt.context"); len(got) != 1 {
		t.Fatalf("events after the retry: %v", got)
	}
}

func TestACancelledSweepRecordsNoContext(t *testing.T) {
	f := newContextFixture(t)
	a, _ := f.claude(110_000)
	w := f.watcher()
	f.sweep(w)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.st.NoteAttempt(cancelled, a.ID, "context", "150000: context 160000"); err == nil {
		t.Fatal("the store wrote under a cancelled context")
	}
	w.noteContext(cancelled, a, 160_000)
	if seen := w.contextSeen[a.ID]; seen == nil || len(seen.marks) != 1 || seen.marks[150_000] {
		t.Fatalf("marks after a cancelled sweep: %+v", seen)
	}
	unknown := f.attempt("copilot", []string{"copilot", "--session-id", harness.NewSessionID()}, t.TempDir())
	w.noteContextUnknown(cancelled, unknown)
	if w.contextSeen[unknown.ID] != nil || len(f.events(a, "attempt.context")) != 1 || len(f.events(unknown, "attempt.context_unknown")) != 0 {
		t.Fatal("a cancelled sweep recorded context")
	}
}

func TestAWorkerWithNoTranscriptRecordsNoContextEvent(t *testing.T) {
	f := newContextFixture(t)
	session := harness.NewSessionID()
	claude := f.attempt("claude", []string{"claude", "--session-id", session, "brief"}, t.TempDir())
	omp := f.attempt("omp", []string{"omp", "--", "You are worker a2 of the Hand fleet f."}, t.TempDir())
	f.vars["PI_CODING_AGENT_DIR"] = ".omp-agent"
	f.sweep(f.watcher())
	for _, a := range []state.Attempt{claude, omp} {
		if got, unknown := f.events(a, "attempt.context"), f.events(a, "attempt.context_unknown"); len(got)+len(unknown) != 0 {
			t.Fatalf("%s: %v, %v", a.Harness, got, unknown)
		}
	}
}

func TestWatcherReadsACodexWorkersContext(t *testing.T) {
	f := newContextFixture(t)
	prompt := "Fix the login bug."
	a := f.attempt("codex", []string{"codex", "-m", "m", prompt}, t.TempDir())
	codex := t.TempDir()
	f.vars["CODEX_HOME"] = codex
	if err := os.MkdirAll(a.Worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(a.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(codex, "sessions", "2026", "11", "18")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"timestamp":"2026-11-18T09:00:01Z","type":"session_meta","payload":{"id":"codex-w","cwd":` + strconv.Quote(real) + `,"timestamp":"2026-11-18T09:00:01Z"}}` + "\n" +
		`{"timestamp":"2026-11-18T09:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":` + strconv.Quote(prompt) + `}]}}` + "\n" +
		`{"timestamp":"2026-11-18T09:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":120000},"model_context_window":258400}}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "rollout-2026-11-18T09-00-01-codex-w.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	f.sweep(f.watcher())
	if got := f.events(a, "attempt.context"); !sameStrings(got, []string{"100000: context 120000"}) {
		t.Fatalf("codex events: %v", got)
	}
	if got := f.events(a, "attempt.context_unknown"); len(got) != 0 {
		t.Fatalf("codex unknown: %v", got)
	}
}
