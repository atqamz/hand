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

func TestWatcherCopiesTheOpencodeModelToTheSupervisorAndTheWorker(t *testing.T) {
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
	a, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: task.ID, Harness: "opencode", Argv: []string{"opencode", "--standalone", "--auto", "--prompt", prompt}}, t.TempDir())
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
	sup, err := st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "opencode", Argv: []string{"opencode"}, Session: "ses_1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t0", PaneID: "0", PID: 2, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	export := opencodeFixture(t, "opencode-assistant.json")
	user := strings.Replace(export, "Implement the change.", prompt, 1)
	list := fmt.Sprintf(`[{"id":"ses_2","created":%d,"directory":%q}]`, now.UnixMilli(), real)
	fakebin.Install(t, bin, "opencode", "ocexport", map[string]string{"export": user, "list": list})
	data := t.TempDir()
	db := filepath.Join(data, "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{"PATH": bin, "HOME": t.TempDir(), "XDG_DATA_HOME": data}
	r := &runner{env: Env{Getenv: func(k string) string { return vars[k] }, Now: func() time.Time { return now }}, home: home, root: t.TempDir(), fleet: state.Fleet{ID: "f"}}
	w := &watcher{r: r, st: st}
	sync := func() {
		w.opencodeModels(ctx)
		w.pending.Wait()
	}
	sync()
	gotSup, _, _ := st.LiveSupervisor(ctx)
	gotAtt, _ := st.Attempt(ctx, a.ID)
	if gotSup.Model != "anthropic/claude-sonnet-5-5" || gotSup.Effort != "" || gotAtt.Model != "anthropic/claude-sonnet-5-5" || gotAtt.Effort != "" {
		t.Fatalf("supervisor = %q %q, attempt = %q %q", gotSup.Model, gotSup.Effort, gotAtt.Model, gotAtt.Effort)
	}
	events, err := st.RecentEventsOf(ctx, []string{"model.changed"}, 10)
	if err != nil || len(events) != 2 {
		t.Fatalf("events = %v, %v", events, err)
	}

	switched := strings.ReplaceAll(user, `"variant":"default"`, `"variant":"high"`)
	fakebin.Install(t, bin, "opencode", "ocexport", map[string]string{"export": switched, "list": list})
	if err := os.Chtimes(db, now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	sync()
	gotSup, _, _ = st.LiveSupervisor(ctx)
	gotAtt, _ = st.Attempt(ctx, a.ID)
	if gotSup.Effort != "high" || gotAtt.Effort != "high" {
		t.Fatalf("after switch supervisor = %q, attempt = %q", gotSup.Effort, gotAtt.Effort)
	}
	sync()
	if events, _ := st.RecentEventsOf(ctx, []string{"model.changed"}, 10); len(events) != 4 {
		t.Fatalf("events after switch = %d", len(events))
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
