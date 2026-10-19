package cli_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/flock"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

const day = 24 * time.Hour

func inboxFile(t *testing.T, h *harness, name string, size int, age time.Duration) {
	t.Helper()
	path := filepath.Join(h.home, "inbox", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, h.now.Add(-age), h.now.Add(-age)); err != nil {
		t.Fatal(err)
	}
}

func pruneEvents(t *testing.T, h *harness) []string {
	t.Helper()
	st, err := state.Open(filepath.Join(h.home, "hand.db"), h.clock)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	events, err := st.RecentEventsOf(context.Background(), []string{"inbox.pruned"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range events {
		out = append(out, e.Detail)
	}
	return out
}

func TestInboxSizeOfAnEmptyAndAFullInbox(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	dir := toon.Value(filepath.Join(h.home, "inbox"))
	if out, want := h.ok("inbox", "size"), "inbox: "+dir+"\nfiles: 0\nsize: 0 MiB\ntrash_files: 0\ntrash_size: 0 MiB\n"; out != want {
		t.Fatalf("empty size = %q, want %q", out, want)
	}
	inboxFile(t, h, "20260801-000000-1.png", 3<<19, 56*day)
	inboxFile(t, h, "20260920-000000-1-report.pdf", 5, 6*day)
	inboxFile(t, h, "20260925-230000-1.png", 5, time.Hour)
	inboxFile(t, h, "notes.txt", 1<<20, 90*day)
	inboxFile(t, h, ".upload-123", 1<<20, 90*day)
	inboxFile(t, h, filepath.Join(".trash", "20260701-000000-1.png"), 1<<20, 2*day)
	if out, want := h.ok("inbox", "size"), "inbox: "+dir+"\nfiles: 3\nsize: 1.5 MiB\noldest: 56d\nnewest: 1h\ntrash_files: 1\ntrash_size: 1 MiB\n"; out != want {
		t.Fatalf("full size = %q, want %q", out, want)
	}
}

func TestInboxPruneNeedsOlderThanInDays(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	inboxFile(t, h, "20260801-000000-1.png", 5, 56*day)
	for args, want := range map[string]string{
		"inbox":                            "usage: hand inbox SUBCOMMAND; subcommands: prune, size",
		"inbox prune":                      "usage: hand inbox prune --older-than DURATION [--dry-run]",
		"inbox prune --dry-run":            "usage: hand inbox prune --older-than DURATION [--dry-run]",
		"inbox prune --older-than 12h":     `--older-than "12h" is not a number of days from 1d to 9999d, like 30d`,
		"inbox prune --older-than 0d":      `--older-than "0d" is not a number of days`,
		"inbox prune --older-than 30d now": "want 0 argument(s)",
	} {
		if _, errOut, code := h.run(strings.Fields(args)...); code != 2 || !strings.Contains(errOut, want) {
			t.Fatalf("hand %s: code=%d stderr=%q, want %q", args, code, errOut, want)
		}
	}
	if _, err := os.Lstat(filepath.Join(h.home, "inbox", "20260801-000000-1.png")); err != nil {
		t.Fatal(err)
	}
}

func TestInboxPruneMovesThenExpiresAndRecordsOneEvent(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	dir := toon.Value(filepath.Join(h.home, "inbox"))
	inboxFile(t, h, "20260801-000000-1.png", 3<<19, 56*day)
	inboxFile(t, h, "20260920-000000-1-report.pdf", 5, 6*day)
	want := "inbox: " + dir + "\ndry_run: true\nmoved: 1\nmoved_size: 1.5 MiB\ndeleted: 0\nfreed: 0 MiB\n" +
		"move[1]{name,age,size}:\n  20260801-000000-1.png,56d,1.5 MiB\n" +
		"delete[0]{name,age,size}:\n"
	if out := h.ok("inbox", "prune", "--older-than", "30d", "--dry-run"); out != want {
		t.Fatalf("dry run = %q, want %q", out, want)
	}
	if _, err := os.Lstat(filepath.Join(h.home, "inbox", ".trash")); !os.IsNotExist(err) || pruneEvents(t, h) != nil {
		t.Fatalf("dry run changed something: %v", err)
	}
	if out, want := h.ok("inbox", "prune", "--older-than", "30d"), "inbox: "+dir+"\nmoved: 1\nmoved_size: 1.5 MiB\ndeleted: 0\nfreed: 0 MiB\n"; out != want {
		t.Fatalf("prune = %q, want %q", out, want)
	}
	if _, err := os.Lstat(filepath.Join(h.home, "inbox", ".trash", "20260801-000000-1.png")); err != nil {
		t.Fatal(err)
	}
	if out, want := h.ok("inbox", "prune", "--older-than", "30d"), "inbox: "+dir+"\nmoved: 0\nmoved_size: 0 MiB\ndeleted: 0\nfreed: 0 MiB\n"; out != want {
		t.Fatalf("second prune = %q, want %q", out, want)
	}
	h.advance(8 * day)
	if out, want := h.ok("inbox", "prune", "--older-than", "30d"), "inbox: "+dir+"\nmoved: 0\nmoved_size: 0 MiB\ndeleted: 1\nfreed: 1.5 MiB\n"; out != want {
		t.Fatalf("expiry = %q, want %q", out, want)
	}
	if got := pruneEvents(t, h); len(got) != 1 || got[0] != "1 file, 1.5 MiB" {
		t.Fatalf("events = %q", got)
	}
	has(t, "orient", h.ok("orient"), ",inbox.pruned,")
}

func TestInboxPruneReportsAFailedFileAndGoesOn(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	inboxFile(t, h, "20260801-000000-1.png", 5, 56*day)
	inboxFile(t, h, "20260802-000000-1.png", 5, 55*day)
	inboxFile(t, h, filepath.Join(".trash", "20260801-000000-1.png", "child"), 5, time.Hour)
	out, errOut, code := h.run("inbox", "prune", "--older-than", "30d")
	if code != 1 || !strings.Contains(errOut, "1 file(s) could not be pruned") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	has(t, "prune", out, "moved: 1\n", "failed[1]{name,error}:\n  20260801-000000-1.png,")
	if got := pruneEvents(t, h); len(got) != 1 || got[0] != "1 file, 0 MiB" {
		t.Fatalf("events = %q", got)
	}
}

func TestInboxPruneTakesTheFleetLock(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	inboxFile(t, h, "20260801-000000-1.png", 5, 56*day)
	if err := os.MkdirAll(filepath.Join(h.home, "locks"), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(h.home, "locks", "inbox.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := flock.Lock(f, false); !ok || err != nil {
		t.Fatalf("lock = %v, %v", ok, err)
	}
	if _, errOut, code := h.run("inbox", "prune", "--older-than", "30d"); code != 3 || !strings.Contains(errOut, "another inbox prune is running") {
		t.Fatalf("held lock: code=%d stderr=%q", code, errOut)
	}
	has(t, "dry run", h.ok("inbox", "prune", "--older-than", "30d", "--dry-run"), "moved: 1\n")
	if err := flock.Release(f); err != nil {
		t.Fatal(err)
	}
	has(t, "prune", h.ok("inbox", "prune", "--older-than", "30d"), "moved: 1\n")
}

func TestInboxPruneStillPrintsWhatMovedWhenTheEventFails(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	inboxFile(t, h, "20260801-000000-1.png", 5, 56*day)
	db, err := sql.Open("sqlite", filepath.Join(h.home, "hand.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER refuse_prune BEFORE INSERT ON event WHEN NEW.kind = 'inbox.pruned' BEGIN SELECT RAISE(ABORT, 'event refused'); END`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := h.run("inbox", "prune", "--older-than", "30d")
	if code != 1 || !strings.Contains(errOut, "event refused") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	has(t, "prune", out, "moved: 1\n")
}
