package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

type inboxClock struct{ now time.Time }

func (c *inboxClock) Now() time.Time { return c.now }

func inboxWatcher(t *testing.T, inbox string) (*watcher, *bytes.Buffer, *inboxClock) {
	t.Helper()
	clock := &inboxClock{time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	home := t.TempDir()
	policy := `{"profiles":{"default":{"harness":"claude","model":"sonnet","effort":"medium"}}` + inbox + `}`
	if err := os.WriteFile(filepath.Join(home, "routing.json"), []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(filepath.Join(home, "hand.db"), clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var out bytes.Buffer
	r := &runner{env: Env{Now: clock.Now, Stdout: &out}, home: home, root: t.TempDir(), fleet: state.Fleet{ID: "f"}}
	return &watcher{r: r, st: st}, &out, clock
}

func oldFile(t *testing.T, w *watcher, name string, age time.Duration) string {
	t.Helper()
	path := filepath.Join(w.r.home, "inbox", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	when := w.r.env.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return path
}

func prunedEvents(t *testing.T, w *watcher) int {
	t.Helper()
	events, err := w.st.RecentEventsOf(context.Background(), []string{"inbox.pruned"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	return len(events)
}

func TestWatcherPrunesTheInboxOnceADay(t *testing.T) {
	w, out, clock := inboxWatcher(t, `,"inbox":{"keep":"30d"}`)
	ctx := context.Background()
	old := oldFile(t, w, "20260901-000000-1.png", 40*24*time.Hour)
	young := oldFile(t, w, "20261018-000000-1.png", 24*time.Hour)
	w.pruneInbox(ctx)
	if _, err := os.Lstat(old); !os.IsNotExist(err) {
		t.Fatalf("old file stayed: %v", err)
	}
	if _, err := os.Lstat(young); err != nil {
		t.Fatal(err)
	}
	events, err := w.st.RecentEventsOf(ctx, []string{"inbox.pruned"}, 10)
	if err != nil || len(events) != 1 || events[0].Detail != "1 file, 0 MiB" {
		t.Fatalf("events = %+v, %v", events, err)
	}
	if out.Len() != 0 {
		t.Fatalf("prune logged %q", out.String())
	}
	again := oldFile(t, w, "20260902-000000-1.png", 40*24*time.Hour)
	restarted := &watcher{r: w.r, st: w.st}
	restarted.pruneInbox(ctx)
	if _, err := os.Lstat(again); err != nil || prunedEvents(t, w) != 1 {
		t.Fatalf("restart pruned again the same day: %v", err)
	}
	clock.now = clock.now.Add(24 * time.Hour)
	restarted.pruneInbox(ctx)
	if _, err := os.Lstat(again); !os.IsNotExist(err) || prunedEvents(t, w) != 2 {
		t.Fatalf("next day did not prune: %v, %d events", err, prunedEvents(t, w))
	}
	clock.now = clock.now.Add(24 * time.Hour)
	restarted.pruneInbox(ctx)
	if n := prunedEvents(t, w); n != 2 || restarted.pruned != "20261021" {
		t.Fatalf("a day with nothing to move: %d events, pruned %q", n, restarted.pruned)
	}
	if out.Len() != 0 {
		t.Fatalf("prune logged %q", out.String())
	}
}

func TestWatcherLeavesTheInboxAloneByDefault(t *testing.T) {
	for _, inbox := range []string{``, `,"inbox":"off"`, `,"inbox":{"keep":"off"}`} {
		w, out, _ := inboxWatcher(t, inbox)
		old := oldFile(t, w, "20250101-000000-1.png", 300*24*time.Hour)
		w.pruneInbox(context.Background())
		if _, err := os.Lstat(old); err != nil || prunedEvents(t, w) != 0 || out.Len() != 0 {
			t.Fatalf("policy %q pruned: %v, %q", inbox, err, out.String())
		}
		if _, err := os.Lstat(filepath.Join(w.r.home, "inbox", ".trash")); !os.IsNotExist(err) {
			t.Fatalf("policy %q made the trash: %v", inbox, err)
		}
	}
}

func TestWatcherSaysOnceThatABadInboxSettingKeepsPruningOff(t *testing.T) {
	w, out, _ := inboxWatcher(t, `,"inbox":{"keep":"soon"}`)
	old := oldFile(t, w, "20250101-000000-1.png", 300*24*time.Hour)
	w.pruneInbox(context.Background())
	w.pruneInbox(context.Background())
	if got := out.String(); strings.Count(got, "inbox pruning stays off") != 1 || !strings.Contains(got, `inbox.keep is \"soon\"`) {
		t.Fatalf("log = %q", got)
	}
	if _, err := os.Lstat(old); err != nil || prunedEvents(t, w) != 0 {
		t.Fatalf("bad setting pruned: %v", err)
	}
}

func TestWatcherRetriesWhileAPruneHoldsTheLock(t *testing.T) {
	w, out, _ := inboxWatcher(t, `,"inbox":{"keep":"30d"}`)
	old := oldFile(t, w, "20260901-000000-1.png", 40*24*time.Hour)
	unlock, ok, err := w.r.lock("inbox", false)
	if !ok || err != nil {
		t.Fatalf("lock = %v, %v", ok, err)
	}
	w.pruneInbox(context.Background())
	if _, err := os.Lstat(old); err != nil || w.pruned != "" || out.Len() != 0 {
		t.Fatalf("held lock: %v, pruned %q, log %q", err, w.pruned, out.String())
	}
	unlock()
	w.pruneInbox(context.Background())
	if _, err := os.Lstat(old); !os.IsNotExist(err) || prunedEvents(t, w) != 1 {
		t.Fatalf("after release: %v", err)
	}
}
