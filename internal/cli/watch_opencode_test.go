package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/state"
)

func opencodeFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "transcript", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

type opencodeRig struct {
	t      *testing.T
	st     *state.Store
	w      *watcher
	a      state.Attempt
	real   string
	prompt string
	now    time.Time
	bin    string
	db     string
	api    string
	count  string
}

func newOpencodeRig(t *testing.T, supervisor, installed bool, launch ...string) *opencodeRig {
	t.Helper()
	ctx := context.Background()
	home, bin := t.TempDir(), t.TempDir()
	now := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	st, err := state.Open(filepath.Join(home, "hand.db"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.CreateFleet(ctx, "f"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddProject(ctx, "hand", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	task, err := st.AddTask(ctx, "hand", "Fix login", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Transition(ctx, task.ID, state.StatusActive); err != nil {
		t.Fatal(err)
	}
	prompt := "Fix the login bug."
	argvPrompt := append(launch, prompt)[0]
	a, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: task.ID, Harness: "opencode", Argv: []string{"opencode", "--standalone", "--auto", "--prompt", argvPrompt}}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(a.Worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(a.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "1", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	if supervisor {
		sup, err := st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "opencode", Argv: []string{"opencode"}, Session: "ses_1"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t0", PaneID: "0", PID: 2, StartMarker: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	data := t.TempDir()
	db := filepath.Join(data, "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	path := bin
	if !installed {
		path = t.TempDir()
	}
	vars := map[string]string{"PATH": path, "HOME": t.TempDir(), "XDG_DATA_HOME": data}
	r := &runner{env: Env{Getenv: func(k string) string { return vars[k] }, Now: func() time.Time { return now }}, home: home, root: t.TempDir(), fleet: state.Fleet{ID: "f"}}
	return &opencodeRig{t: t, st: st, w: &watcher{r: r, st: st}, a: a, real: real, prompt: prompt, now: now, bin: bin, db: db, count: filepath.Join(t.TempDir(), "count")}
}

func (g *opencodeRig) install(export, list string) {
	g.t.Helper()
	fakebin.Install(g.t, g.bin, "opencode", "ocexport", map[string]string{"export": export, "list": list, "api": g.api, "count": g.count})
}

func (g *opencodeRig) calls(kind string) int {
	b, _ := os.ReadFile(g.count)
	return strings.Count(string(b), kind+"\n")
}

func (g *opencodeRig) listed(dir string, created time.Time) string {
	return fmt.Sprintf(`[{"id":"ses_2","created":%d,"directory":%q}]`, created.UnixMilli(), dir)
}

func (g *opencodeRig) sync() {
	g.w.opencodeModels(context.Background())
	g.w.pending.Wait()
}

func (g *opencodeRig) attempt() state.Attempt {
	g.t.Helper()
	a, err := g.st.Attempt(context.Background(), g.a.ID)
	if err != nil {
		g.t.Fatal(err)
	}
	return a
}

func (g *opencodeRig) changes() int {
	g.t.Helper()
	events, err := g.st.RecentEventsOf(context.Background(), []string{"model.changed"}, 10)
	if err != nil {
		g.t.Fatal(err)
	}
	return len(events)
}

func TestWatcherCopiesTheOpencodeModelToTheSupervisorAndTheWorker(t *testing.T) {
	ctx := context.Background()
	g := newOpencodeRig(t, true, true)
	user := strings.Replace(opencodeFixture(t, "opencode-assistant.json"), "Implement the change.", g.prompt, 1)
	list := g.listed(g.real, g.now)
	g.install(user, list)
	g.sync()
	gotSup, _, _ := g.st.LiveSupervisor(ctx)
	gotAtt := g.attempt()
	if gotSup.Model != "anthropic/claude-sonnet-5-5" || gotSup.Effort != "" || gotAtt.Model != "anthropic/claude-sonnet-5-5" || gotAtt.Effort != "" {
		t.Fatalf("supervisor = %q %q, attempt = %q %q", gotSup.Model, gotSup.Effort, gotAtt.Model, gotAtt.Effort)
	}
	if n := g.changes(); n != 2 {
		t.Fatalf("events = %d", n)
	}

	switched := strings.ReplaceAll(user, `"variant":"default"`, `"variant":"high"`)
	g.install(switched, list)
	if err := os.Chtimes(g.db, g.now, g.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	g.sync()
	gotSup, _, _ = g.st.LiveSupervisor(ctx)
	gotAtt = g.attempt()
	if gotSup.Effort != "high" || gotAtt.Effort != "high" {
		t.Fatalf("after switch supervisor = %q, attempt = %q", gotSup.Effort, gotAtt.Effort)
	}
	g.sync()
	if n := g.changes(); n != 4 {
		t.Fatalf("events after switch = %d", n)
	}
}

func TestWatcherPicksNoWorkerSessionThatIsNotTheAttempts(t *testing.T) {
	assistant := opencodeFixture(t, "opencode-assistant.json")
	for name, c := range map[string]struct {
		dir     func(g *opencodeRig) string
		created time.Duration
		prompt  string
	}{
		"another directory":  {dir: func(*opencodeRig) string { return t.TempDir() }, prompt: "Fix the login bug."},
		"created before":     {dir: func(g *opencodeRig) string { return g.real }, created: -time.Hour, prompt: "Fix the login bug."},
		"a different prompt": {dir: func(g *opencodeRig) string { return g.real }, prompt: "Write the docs."},
	} {
		t.Run(name, func(t *testing.T) {
			g := newOpencodeRig(t, false, true)
			user := strings.Replace(assistant, "Implement the change.", c.prompt, 1)
			g.install(user, g.listed(c.dir(g), g.now.Add(c.created)))
			g.sync()
			if got := g.attempt(); got.Model != "" || g.changes() != 0 {
				t.Fatalf("attempt model = %q, events = %d", got.Model, g.changes())
			}
		})
	}
}

func TestWatcherFindsTheWorkerSessionBehindAQuotedPrompt(t *testing.T) {
	g := newOpencodeRig(t, false, true, "\" \tFix the login bug.")
	g.install(strings.Replace(opencodeFixture(t, "opencode-assistant.json"), "Implement the change.", g.prompt, 1), g.listed(g.real, g.now))
	g.sync()
	if got := g.attempt(); got.Model != "anthropic/claude-sonnet-5-5" {
		t.Fatalf("attempt model = %q", got.Model)
	}
}

func TestWatcherWritesNothingWhenOpencodeCannotExport(t *testing.T) {
	g := newOpencodeRig(t, false, true)
	g.install("not json", g.listed(g.real, g.now))
	g.sync()
	if got := g.attempt(); got.Model != "" || g.changes() != 0 {
		t.Fatalf("attempt model = %q, events = %d", got.Model, g.changes())
	}
}

func TestWatcherWritesNothingWithoutOpencode(t *testing.T) {
	g := newOpencodeRig(t, true, false)
	g.install(opencodeFixture(t, "opencode-assistant.json"), g.listed(g.real, g.now))
	g.sync()
	if got := g.attempt(); got.Model != "" || g.changes() != 0 {
		t.Fatalf("attempt model = %q, events = %d", got.Model, g.changes())
	}
}

func TestWatcherFallsBackToTheSessionModel(t *testing.T) {
	g := newOpencodeRig(t, false, true)
	g.install(strings.Replace(opencodeFixture(t, "opencode-info-model.json"), "Implement the change.", g.prompt, 1), g.listed(g.real, g.now))
	g.sync()
	if got := g.attempt(); got.Model != "anthropic/claude-sonnet-5-5" || got.Effort != "" || g.changes() != 1 {
		t.Fatalf("attempt = %q %q, events = %d", got.Model, got.Effort, g.changes())
	}
}

func TestWatcherStopsLookingForAWorkerSession(t *testing.T) {
	g := newOpencodeRig(t, false, true)
	g.install(opencodeFixture(t, "opencode-assistant.json"), g.listed(g.real, g.now))
	for range lookupSweeps {
		g.sync()
	}
	if g.w.opencodeMisses[g.a.ID] != lookupSweeps {
		t.Fatalf("misses = %d", g.w.opencodeMisses[g.a.ID])
	}
	g.install(strings.Replace(opencodeFixture(t, "opencode-assistant.json"), "Implement the change.", g.prompt, 1), g.listed(g.real, g.now))
	g.sync()
	if got := g.attempt(); got.Model != "" {
		t.Fatalf("attempt model = %q after giving up", got.Model)
	}
}

func TestWatcherFindsTheSessionOfAnOldAttemptOnAFreshWatcher(t *testing.T) {
	g := newOpencodeRig(t, false, true)
	g.install(strings.Replace(opencodeFixture(t, "opencode-assistant.json"), "Implement the change.", g.prompt, 1), g.listed(g.real, g.now))
	later := g.now.Add(time.Hour)
	g.w.r.env.Now = func() time.Time { return later }
	g.sync()
	if got := g.attempt(); got.Model != "anthropic/claude-sonnet-5-5" {
		t.Fatalf("attempt model = %q", got.Model)
	}
}

func TestWatcherFindsASessionThatAppearsAfterEmptySweeps(t *testing.T) {
	g := newOpencodeRig(t, false, true)
	g.install(opencodeFixture(t, "opencode-assistant.json"), "[]")
	for range lookupSweeps - 1 {
		g.sync()
	}
	if g.w.opencodeMisses[g.a.ID] != lookupSweeps-1 {
		t.Fatalf("misses = %d", g.w.opencodeMisses[g.a.ID])
	}
	g.install(strings.Replace(opencodeFixture(t, "opencode-assistant.json"), "Implement the change.", g.prompt, 1), g.listed(g.real, g.now))
	g.sync()
	if got := g.attempt(); got.Model != "anthropic/claude-sonnet-5-5" {
		t.Fatalf("attempt model = %q", got.Model)
	}
}

func TestWatcherCountsNoMissWhenTheLookupFails(t *testing.T) {
	g := newOpencodeRig(t, false, true)
	for name, c := range map[string]struct{ export, list string }{
		"list":   {opencodeFixture(t, "opencode-assistant.json"), "not json"},
		"export": {"not json", g.listed(g.real, g.now)},
	} {
		g.install(c.export, c.list)
		for range lookupSweeps + 1 {
			g.sync()
		}
		if g.w.opencodeMisses[g.a.ID] != 0 {
			t.Fatalf("%s: misses = %d", name, g.w.opencodeMisses[g.a.ID])
		}
	}
}

func TestWatcherForgetsTheTranscriptOfAChangedSupervisorSession(t *testing.T) {
	ctx := context.Background()
	g := newOpencodeRig(t, true, true)
	g.install(opencodeFixture(t, "opencode-assistant.json"), g.listed(g.real, g.now))
	g.sync()
	if g.w.transcripts.Status("opencode", "ses_1").Model == "" {
		t.Fatal("status not read")
	}
	sup, _, err := g.st.LiveSupervisor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.st.SetSupervisorSession(ctx, sup.ID, "ses_3"); err != nil {
		t.Fatal(err)
	}
	g.sync()
	if g.w.transcripts.Status("opencode", "ses_1").Model != "" {
		t.Fatal("old supervisor session kept")
	}
	if g.w.transcripts.Status("opencode", "ses_3").Model == "" {
		t.Fatal("new supervisor session not read")
	}
}

func TestWatcherReadsTheModelsFromOneApiCallPerSweep(t *testing.T) {
	ctx := context.Background()
	g := newOpencodeRig(t, true, true)
	g.api = `{"data":[{"id":"ses_1","model":{"id":"gpt-5","providerID":"openai","variant":"high"}},{"id":"ses_2","model":{"id":"gpt-5","providerID":"openai","variant":"default"}},{"id":"ses_9","model":null}],"cursor":{}}`
	g.install(strings.Replace(opencodeFixture(t, "opencode-assistant.json"), "Implement the change.", g.prompt, 1), g.listed(g.real, g.now))
	for range 3 {
		g.sync()
	}
	sup, _, _ := g.st.LiveSupervisor(ctx)
	got := g.attempt()
	if sup.Model != "openai/gpt-5" || sup.Effort != "high" || got.Model != "openai/gpt-5" || got.Effort != "" {
		t.Fatalf("supervisor = %q %q, attempt = %q %q", sup.Model, sup.Effort, got.Model, got.Effort)
	}
	if g.calls("api") != 3 || g.calls("export") != 1 {
		t.Fatalf("api = %d, export = %d", g.calls("api"), g.calls("export"))
	}
}

func TestWatcherExportsASessionWithoutAnApiModelOnce(t *testing.T) {
	g := newOpencodeRig(t, false, true)
	g.api = `{"data":[{"id":"ses_2","model":null}]}`
	g.install(strings.Replace(opencodeFixture(t, "opencode-assistant.json"), "Implement the change.", g.prompt, 1), g.listed(g.real, g.now))
	for range 3 {
		g.sync()
	}
	if got := g.attempt(); got.Model != "anthropic/claude-sonnet-5-5" {
		t.Fatalf("attempt model = %q", got.Model)
	}
	if g.calls("export") != 2 {
		t.Fatalf("export = %d", g.calls("export"))
	}
}

func TestWatcherReadsAnApiNullSessionAgainUntilTheExportHasAModel(t *testing.T) {
	g := newOpencodeRig(t, false, true)
	g.api = `{"data":[{"id":"ses_2","model":null}]}`
	assistant := strings.Replace(opencodeFixture(t, "opencode-assistant.json"), "Implement the change.", g.prompt, 1)
	userOnly := `{"info":{"id":"ses_2","model":null},"messages":[{"id":"msg_1","type":"user","time":{"created":1793260115879},"text":"` + g.prompt + `"}]}`
	list := g.listed(g.real, g.now)
	g.install(userOnly, list)
	g.sync()
	if got := g.attempt(); got.Model != "" {
		t.Fatalf("attempt model = %q before a reply", got.Model)
	}
	g.install(assistant, list)
	if err := os.Chtimes(g.db, g.now, g.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	g.sync()
	if got := g.attempt(); got.Model != "anthropic/claude-sonnet-5-5" {
		t.Fatalf("attempt model = %q after a reply", got.Model)
	}
}

func TestWatcherExportsASessionTheApiDoesNotList(t *testing.T) {
	g := newOpencodeRig(t, false, true)
	g.api = `{"data":[{"id":"ses_9","model":{"id":"gpt-5","providerID":"openai"}}]}`
	g.install(strings.Replace(opencodeFixture(t, "opencode-assistant.json"), "Implement the change.", g.prompt, 1), g.listed(g.real, g.now))
	g.sync()
	if got := g.attempt(); got.Model != "anthropic/claude-sonnet-5-5" {
		t.Fatalf("attempt model = %q", got.Model)
	}
}

func TestWatcherFallsBackToTheExportWhenTheApiFails(t *testing.T) {
	for name, api := range map[string]string{"exits": "", "not json": "not json", "no data": `{"cursor":{}}`, "data is not a list": `{"data":"x"}`} {
		t.Run(name, func(t *testing.T) {
			g := newOpencodeRig(t, true, true)
			g.api = api
			g.install(strings.Replace(opencodeFixture(t, "opencode-assistant.json"), "Implement the change.", g.prompt, 1), g.listed(g.real, g.now))
			g.sync()
			g.sync()
			sup, _, _ := g.st.LiveSupervisor(context.Background())
			if got := g.attempt(); got.Model != "anthropic/claude-sonnet-5-5" || sup.Model != "anthropic/claude-sonnet-5-5" {
				t.Fatalf("attempt model = %q, supervisor model = %q", got.Model, sup.Model)
			}
		})
	}
}

func TestWatcherForgetsTheTranscriptsOfEndedSessions(t *testing.T) {
	ctx := context.Background()
	g := newOpencodeRig(t, false, true)
	g.install(strings.Replace(opencodeFixture(t, "opencode-assistant.json"), "Implement the change.", g.prompt, 1), g.listed(g.real, g.now))
	g.sync()
	if g.w.transcripts.Status("opencode", "ses_2").Model == "" {
		t.Fatal("status not read")
	}
	if _, err := g.st.EndAttempt(ctx, g.a.ID, state.AttemptExited, "done"); err != nil {
		t.Fatal(err)
	}
	g.sync()
	if g.w.transcripts.Status("opencode", "ses_2").Model != "" || len(g.w.opencodeSessions) != 0 {
		t.Fatal("session kept")
	}
}

func TestLastSpecDropsTheModelOpencodeReported(t *testing.T) {
	got := lastSpec(state.Supervisor{Harness: "opencode", Model: "anthropic/claude-sonnet-5-5", Effort: "high"})
	if got.Model != "" || got.Effort != "" {
		t.Fatalf("spec = %+v", got)
	}
	if got := lastSpec(state.Supervisor{Harness: "claude", Model: "sonnet", Effort: "high"}); got.Model != "sonnet" || got.Effort != "high" {
		t.Fatalf("spec = %+v", got)
	}
}
