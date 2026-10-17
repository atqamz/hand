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

func snapshotWatcher(t *testing.T) (*watcher, *bytes.Buffer, string) {
	t.Helper()
	now := func() time.Time { return time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC) }
	st, err := state.Open(filepath.Join(t.TempDir(), "hand.db"), now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var out bytes.Buffer
	root := t.TempDir()
	r := &runner{env: Env{Now: now, Stdout: &out}, root: root, fleet: state.Fleet{ID: "f"}}
	return &watcher{r: r, st: st}, &out, filepath.Join(root, "backups", "f")
}

func TestSnapshotCancelledIsNotAFailureAndIsRetried(t *testing.T) {
	w, out, dir := snapshotWatcher(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.snapshot(ctx)
	if out.Len() != 0 {
		t.Fatalf("cancelled snapshot logged %q", out.String())
	}
	if des, _ := os.ReadDir(dir); len(des) != 0 {
		t.Fatalf("cancelled snapshot left %v", des)
	}
	w.snapshot(context.Background())
	if _, err := os.Stat(filepath.Join(dir, "hand-20260926.db")); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotFailureStillLogsAndCountsAsTried(t *testing.T) {
	w, out, dir := snapshotWatcher(t)
	if err := os.MkdirAll(filepath.Join(dir, "hand-20260926.db.tmp", "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	w.snapshot(context.Background())
	if !strings.Contains(out.String(), "snapshot: ") {
		t.Fatalf("failure not logged: %q", out.String())
	}
	out.Reset()
	w.snapshot(context.Background())
	if out.Len() != 0 {
		t.Fatalf("failed day was tried again: %q", out.String())
	}
}
