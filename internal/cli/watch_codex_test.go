package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

func TestWatcherCopiesTheCodexModelAndEffortAndFollowsASwitch(t *testing.T) {
	ctx := context.Background()
	home, codex := t.TempDir(), t.TempDir()
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
	a, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: task.ID, Harness: "codex", Model: "gpt-5.5", Effort: "high", Argv: []string{"codex", "-m", "gpt-5.5", prompt}}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(a.Worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "1", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	sup, err := st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "codex", Model: "gpt-5.5", Effort: "high", Argv: []string{"codex"}, Session: "codex-sup"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t0", PaneID: "0", PID: 2, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(a.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(codex, "sessions", "2026", "10", "29")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	quoted := func(s string) string {
		b, _ := json.Marshal(s)
		return string(b)
	}
	line := func(s string) string { return s + "\n" }
	meta := func(id, cwd string) string {
		return line(`{"timestamp":"2026-10-29T09:00:01Z","type":"session_meta","payload":{"id":"` + id + `","cwd":` + quoted(cwd) + `,"timestamp":"2026-10-29T09:00:01Z"}}`)
	}
	user := line(`{"timestamp":"2026-10-29T09:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + prompt + `"}]}}`)
	turn := func(model, effort string) string {
		return line(`{"timestamp":"2026-10-29T09:00:03Z","type":"turn_context","payload":{"model":"` + model + `","effort":"` + effort + `"}}`)
	}
	worker := filepath.Join(dir, "rollout-2026-10-29T09-00-01-codex-w.jsonl")
	supervisor := filepath.Join(dir, "rollout-2026-10-29T09-00-01-codex-sup.jsonl")
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(worker, meta("codex-w", real)+user+turn("gpt-5.5", "high"))
	write(supervisor, meta("codex-sup", "/fleet")+turn("gpt-5.5", "high"))
	vars := map[string]string{"CODEX_HOME": codex, "HOME": t.TempDir()}
	r := &runner{env: Env{Getenv: func(k string) string { return vars[k] }, Now: func() time.Time { return now }}, home: home, root: t.TempDir(), fleet: state.Fleet{ID: "f"}}
	w := &watcher{r: r, st: st}
	sweep := func() {
		w.sweepModels(ctx)
		w.pending.Wait()
	}
	models := func() [4]string {
		gotSup, _, _ := st.LiveSupervisor(ctx)
		gotAtt, err := st.Attempt(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		return [4]string{gotSup.Model, gotSup.Effort, gotAtt.Model, gotAtt.Effort}
	}
	changes := func() int {
		events, err := st.RecentEventsOf(ctx, []string{"model.changed"}, 10)
		if err != nil {
			t.Fatal(err)
		}
		return len(events)
	}

	sweep()
	if got := models(); got != [4]string{"gpt-5.5", "high", "gpt-5.5", "high"} || changes() != 0 {
		t.Fatalf("models = %q, events = %d", got, changes())
	}

	write(worker, meta("codex-w", real)+user+turn("gpt-5.5", "high")+turn("gpt-5.4-mini", "low"))
	write(supervisor, meta("codex-sup", "/fleet")+turn("gpt-5.5", "high")+turn("gpt-5.4", "medium"))
	sweep()
	if got := models(); got != [4]string{"gpt-5.4", "medium", "gpt-5.4-mini", "low"} || changes() != 2 {
		t.Fatalf("after a switch models = %q, events = %d", got, changes())
	}
	sweep()
	if changes() != 2 {
		t.Fatalf("events after an unchanged sweep = %d", changes())
	}
}
