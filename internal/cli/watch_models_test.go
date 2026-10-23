package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/state"
)

type modelsClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *modelsClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *modelsClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func modelsWatcher(t *testing.T) (*watcher, *bytes.Buffer, string, *modelsClock) {
	t.Helper()
	bin, root := t.TempDir(), t.TempDir()
	vars := map[string]string{"PATH": bin, "HOME": t.TempDir(), "SECONDHAND_HOME": root, "CODEX_HOME": t.TempDir()}
	var environ []string
	for k, v := range vars {
		environ = append(environ, k+"="+v)
	}
	clock := &modelsClock{now: time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)}
	var out bytes.Buffer
	r := &runner{env: Env{Getenv: func(k string) string { return vars[k] }, Environ: func() []string { return environ }, Now: clock.Now, Stdout: &out}, home: t.TempDir(), root: root, fleet: state.Fleet{ID: "f"}}
	return &watcher{r: r}, &out, bin, clock
}

func runs(t *testing.T, log string) int {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		return 0
	}
	return len(strings.Split(strings.TrimSpace(string(b)), "\n"))
}

func TestWatcherRefreshesDueListsAndLogsEachFailureOnce(t *testing.T) {
	w, out, bin, clock := modelsWatcher(t)
	logs := map[string]string{}
	for _, c := range []struct{ name, stdout string }{{"agy", "gemini-3.8-flash-low\tGemini\n"}, {"claude", "{garbage\n"}} {
		logs[c.name] = filepath.Join(t.TempDir(), c.name+".log")
		fakebin.Install(t, bin, c.name, "lister", map[string]string{"stdout": c.stdout, "log": logs[c.name], "version": c.name + " 1"})
	}
	ctx := context.Background()
	refresh := func() {
		w.refreshModels(ctx)
		w.pending.Wait()
	}
	refresh()
	for name, log := range logs {
		if n := runs(t, log); n != 1 {
			t.Fatalf("%s ran %d times", name, n)
		}
	}
	root := filepath.Join(w.r.root, "models")
	for _, name := range []string{"agy.json", "claude.json"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"codex.json", "opencode.json"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			t.Fatalf("%s written for a harness that is not installed", name)
		}
	}
	if got := out.String(); strings.Count(got, "observed: ") != 1 || !strings.Contains(got, "models claude: claude gave no initialize response") {
		t.Fatalf("log = %q", got)
	}
	refresh()
	if runs(t, logs["claude"]) != 1 || runs(t, logs["agy"]) != 1 {
		t.Fatal("a list that is not due was run again")
	}
	clock.advance(16 * time.Minute)
	refresh()
	if runs(t, logs["claude"]) != 2 || runs(t, logs["agy"]) != 1 {
		t.Fatalf("after the retry delay claude ran %d times and agy %d", runs(t, logs["claude"]), runs(t, logs["agy"]))
	}
	if strings.Count(out.String(), "observed: ") != 1 {
		t.Fatalf("the same failure was logged twice: %q", out.String())
	}
	fakebin.Install(t, bin, "claude", "lister", map[string]string{"stdout": "{garbage\n", "log": logs["claude"], "exit": "1"})
	clock.advance(16 * time.Minute)
	refresh()
	if got := out.String(); strings.Count(got, "observed: ") != 2 || !strings.Contains(got, "models claude: claude -p exited with code 1") {
		t.Fatalf("a new failure was not logged: %q", got)
	}
}

func TestWatcherRunsOneRefreshAtATimeAndStopsWithItsContext(t *testing.T) {
	w, out, bin, _ := modelsWatcher(t)
	log := filepath.Join(t.TempDir(), "log")
	fakebin.Install(t, bin, "agy", "lister", map[string]string{"stdout": "gemini-3.8-flash-low\tGemini\n", "log": log, "sleep": "500ms"})
	w.refreshModels(context.Background())
	w.refreshModels(context.Background())
	w.pending.Wait()
	if n := runs(t, log); n != 1 {
		t.Fatalf("two overlapping calls ran agy %d times", n)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := os.RemoveAll(filepath.Join(w.r.root, "models")); err != nil {
		t.Fatal(err)
	}
	w.refreshModels(cancelled)
	w.pending.Wait()
	if _, err := os.Stat(filepath.Join(w.r.root, "models", "agy.json")); err == nil || out.Len() != 0 {
		t.Fatalf("a cancelled refresh wrote a cache or logged: %v %q", err, out.String())
	}
}
