package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
)

func TestWatcherRecordsTheServedClaudeAndCopilotModel(t *testing.T) {
	ctx := context.Background()
	home, claude, copilot := t.TempDir(), t.TempDir(), t.TempDir()
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
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
	attempt := func(title, name, model, effort string) (state.Attempt, string) {
		t.Helper()
		task, err := st.AddTask(ctx, "hand", title, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Transition(ctx, task.ID, state.StatusActive); err != nil {
			t.Fatal(err)
		}
		session := harness.NewSessionID()
		argv := []string{name, "--session-id", session, "--model", model, "brief"}
		a, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: task.ID, Harness: name, Model: model, Effort: effort, Argv: argv}, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t" + title, PaneID: "1", PID: 1, StartMarker: "m"}); err != nil {
			t.Fatal(err)
		}
		return a, session
	}
	worker, workerID := attempt("alias", "claude", "sonnet", "low")
	fallback, fallbackID := attempt("fallback", "claude", "opus", "high")
	gateway, gatewayID := attempt("gateway", "copilot", "auto", "")
	supSession := harness.NewSessionID()
	sup, err := st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"claude"}, Session: supSession})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t0", PaneID: "0", PID: 2, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}

	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	add := func(path, body string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(body); err != nil {
			t.Fatal(err)
		}
	}
	claudeTurn := func(at, model, effort string) string {
		return `{"type":"assistant","timestamp":"` + at + `","effort":"` + effort + `","perTurnEffort":"` + effort + `","message":{"model":"` + model + `","content":[]}}` + "\n"
	}
	claudeAsked := func(at, requested, served, effort string) string {
		return `{"type":"assistant","timestamp":"` + at + `","requestedModel":"` + requested + `","effort":"` + effort + `","message":{"model":"` + served + `","content":[]}}` + "\n"
	}
	synthetic := `{"type":"assistant","timestamp":"2026-11-01T09:00:09Z","isApiErrorMessage":true,"message":{"model":"<synthetic>","content":[]}}` + "\n"
	copilotTurn := func(at, model string) string {
		return `{"type":"assistant.message","timestamp":"` + at + `","data":{"model":"` + model + `","content":""}}` + "\n"
	}
	claudeFile := func(id string) string { return filepath.Join(claude, "projects", "p", id+".jsonl") }
	supFile, workerFile, fallbackFile := claudeFile(supSession), claudeFile(workerID), claudeFile(fallbackID)
	gatewayFile := filepath.Join(copilot, "session-state", gatewayID, "events.jsonl")

	vars := map[string]string{"CLAUDE_CONFIG_DIR": claude, "COPILOT_HOME": copilot, "HOME": t.TempDir()}
	r := &runner{env: Env{Getenv: func(k string) string { return vars[k] }, Now: func() time.Time { return now }}, home: home, root: t.TempDir(), fleet: state.Fleet{ID: "f"}}
	w := &watcher{r: r, st: st}
	sweep := func() {
		w.sweepModels(ctx)
		w.pending.Wait()
	}
	models := func() [8]string {
		gotSup, _, _ := st.LiveSupervisor(ctx)
		var out [8]string
		out[0], out[1] = gotSup.Model, gotSup.Effort
		for i, id := range []int64{worker.ID, fallback.ID, gateway.ID} {
			a, err := st.Attempt(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			out[2+i*2], out[3+i*2] = a.Model, a.Effort
		}
		return out
	}
	changes := func() int {
		events, err := st.RecentEventsOf(ctx, []string{"model.changed"}, 20)
		if err != nil {
			t.Fatal(err)
		}
		return len(events)
	}

	write(supFile, claudeTurn("2026-11-01T09:00:01Z", "claude-sonnet-5-5", "low")+synthetic)
	write(workerFile, claudeTurn("2026-11-01T09:00:01Z", "claude-sonnet-5-5", "low"))
	write(fallbackFile, claudeTurn("2026-11-01T09:00:01Z", "claude-sonnet-5-5", "high"))
	write(gatewayFile, copilotTurn("2026-11-01T09:00:01Z", "gpt-5.6-luna"))
	sweep()
	want := [8]string{"sonnet", "low", "sonnet", "low", "claude-sonnet-5-5", "high", "gpt-5.6-luna", ""}
	if got := models(); got != want || changes() != 2 {
		t.Fatalf("models = %q, events = %d", got, changes())
	}
	sweep()
	if changes() != 2 {
		t.Fatalf("events after an unchanged sweep = %d", changes())
	}
	if _, err := st.EndAttempt(ctx, worker.ID, state.AttemptStopped, "done"); err != nil {
		t.Fatal(err)
	}
	tracks, err := st.TrackRecord(ctx)
	if err != nil || len(tracks) != 1 || tracks[0].Model != "sonnet" || tracks[0].Effort != "low" || tracks[0].N != 1 {
		t.Fatalf("track = %+v, %v", tracks, err)
	}

	add(supFile, claudeAsked("2026-11-01T09:00:10Z", "claude-opus-5-5[1m]", "claude-opus-5-5", "high"))
	add(fallbackFile, `{"type":"assistant","timestamp":"2026-11-01T09:00:10Z","message":{"model":"claude-haiku-5-5","content":[]}}`+"\n")
	sweep()
	want = [8]string{"claude-opus-5-5[1m]", "high", "sonnet", "low", "claude-haiku-5-5", "high", "gpt-5.6-luna", ""}
	if got := models(); got != want || changes() != 4 {
		t.Fatalf("after a switch models = %q, events = %d", got, changes())
	}

	add(supFile, claudeAsked("2026-11-01T09:00:11Z", "claude-opus-5-5[1m]", "claude-haiku-5-5", "high"))
	add(fallbackFile, claudeAsked("2026-11-01T09:00:11Z", "Claude Sonnet 4.6", "Claude Sonnet 4.6", "high"))
	sweep()
	if got := models(); got != want || changes() != 4 {
		t.Fatalf("after a fallback reply and an invalid model models = %q, events = %d", got, changes())
	}
	add(supFile, claudeAsked("2026-11-01T09:00:12Z", "claude-opus-5-5[1m]", "claude-opus-5-5", "high"))
	sweep()
	if got := models(); got != want || changes() != 4 {
		t.Fatalf("after the return from the fallback models = %q, events = %d", got, changes())
	}

	if _, err := st.EndSupervisor(ctx, sup.ID, state.AttemptStopped, "switch"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Minute)
	next, err := st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"claude"}, Session: supSession})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SupervisorRunning(ctx, next.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t1", PaneID: "2", PID: 3, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	sweep()
	if got := models(); got[0] != "sonnet" || got[1] != "low" {
		t.Fatalf("a row newer than the turn models = %q", got)
	}
	add(supFile, `{"type":"assistant","message":{"model":"claude-haiku-5-5","content":[]}}`+"\n")
	sweep()
	if got := models(); got[0] != "sonnet" || got[1] != "low" {
		t.Fatalf("a reply without a timestamp models = %q", got)
	}
	got, _, _ := st.LiveSupervisor(ctx)
	if spec := lastSpec(got); spec != (harness.Spec{Harness: "claude", Model: "sonnet", Effort: "low"}) {
		t.Fatalf("resume spec = %+v", spec)
	}
}
