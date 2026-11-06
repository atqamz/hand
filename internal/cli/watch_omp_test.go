package cli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

func writeOmpSession(t *testing.T, agent, id, cwd, marker, model, effort string, at time.Time) {
	t.Helper()
	stamp := at.UTC().Format(time.RFC3339Nano)
	user := ""
	if marker != "" {
		user = `{"type":"message","timestamp":"` + stamp + `","message":{"role":"user","content":[{"type":"text","text":` + strconv.Quote(marker) + `}]}}` + "\n"
	}
	body := `{"type":"session","version":3,"id":"` + id + `","timestamp":"` + stamp + `","cwd":` + strconv.Quote(cwd) + `}` + "\n" +
		user +
		`{"type":"thinking_level_change","timestamp":"` + stamp + `","thinkingLevel":` + strconv.Quote(effort) + `}` + "\n" +
		`{"type":"message","timestamp":"` + stamp + `","message":{"role":"assistant","content":[{"type":"text","text":"ok"}],"provider":"acme","model":` + strconv.Quote(model) + `,"stopReason":"stop","usage":{"input":1}}}` + "\n"
	dir := filepath.Join(agent, "sessions", "-fleet")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, at.UTC().Format("2006-01-02T15-04-05-000Z")+"_"+id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWatcherFindsOmpSessionsUnderRelativeAgentDirs(t *testing.T) {
	ctx := context.Background()
	home, worktree := t.TempDir(), t.TempDir()
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	st, err := state.Open(filepath.Join(home, "hand.db"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.CreateFleet(ctx, "f"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddProject(ctx, "hand", worktree, ""); err != nil {
		t.Fatal(err)
	}
	task, err := st.AddTask(ctx, "hand", "Fix login", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Transition(ctx, task.ID, state.StatusActive); err != nil {
		t.Fatal(err)
	}
	supSession, workerSession := "01a1abdf-088f-707d-8973-c784c54b54f1", "01a1abdf-088f-707d-8973-c784c54b54f2"
	sup, err := st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "omp", Model: "asked/sup", Effort: "low", Argv: []string{"omp"}, Session: supSession})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t0", PaneID: "0", PID: 2, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	marker := "You are worker a1 of the Hand fleet f."
	a, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: task.ID, Harness: "omp", Model: "asked/work", Effort: "low", Argv: []string{"omp", "--", marker}}, worktree)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t1", PaneID: "1", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}

	vars := map[string]string{"HOME": t.TempDir(), "PI_CODING_AGENT_DIR": ".omp-agent"}
	r := &runner{env: Env{Getenv: func(k string) string { return vars[k] }, Now: func() time.Time { return now }}, home: home, root: t.TempDir(), fleet: state.Fleet{ID: "f"}}
	w := &watcher{r: r, st: st}
	w.transcripts = &transcript.Reader{Paths: transcript.Paths{}}
	w.modelSessions = map[int64]modelSession{}
	w.sources = map[string]modelSource{"omp": &ompSource{transcriptSource{w, "omp"}}}
	writeOmpSession(t, filepath.Join(home, ".omp-agent"), supSession, home, "", "served/sup", "high", now.Add(time.Second))
	writeOmpSession(t, filepath.Join(a.Worktree, ".omp-agent"), workerSession, a.Worktree, marker, "served/work", "max", now.Add(time.Second))

	w.sweepModels(ctx)
	w.pending.Wait()

	if got, _, _ := st.LiveSupervisor(ctx); got.Model != "acme/served/sup" || got.Effort != "low" {
		t.Fatalf("supervisor model = %s %s", got.Model, got.Effort)
	}
	if got, _ := st.Attempt(ctx, a.ID); got.Model != "acme/served/work" || got.Effort != "low" {
		t.Fatalf("worker model = %s %s", got.Model, got.Effort)
	}
	if id := w.sources["omp"].attemptSession(ctx, a); id != workerSession {
		t.Fatalf("attempt session = %q", id)
	}
}
