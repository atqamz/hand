package cli_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fleetFile(t *testing.T, h *harness, rel string, size int, age time.Duration) {
	t.Helper()
	path := filepath.Join(h.home, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, h.now.Add(-age), h.now.Add(-age)); err != nil {
		t.Fatal(err)
	}
}

func homeHas(t *testing.T, h *harness, rel string) bool {
	t.Helper()
	_, err := os.Lstat(filepath.Join(h.home, filepath.FromSlash(rel)))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func TestGCListsDeletesNothingWithoutYes(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	fleetFile(t, h, "reports/t1/a.md", 100, 40*day)
	fleetFile(t, h, "reports/t1/sub/b.md", 50, 35*day)
	fleetFile(t, h, "reports/t2/c.md", 10, 2*day)
	fleetFile(t, h, "shots/big.png", 1000, 31*day)
	fleetFile(t, h, "memory/old.md", 7, 400*day)
	fleetFile(t, h, "briefs/old.md", 7, 400*day)
	want := "older_than: 30d\napplied: false\n" +
		"candidates[2]{path,files,bytes,oldest}:\n  shots/big.png,1,1000,2026-08-26\n  reports/t1,2,150,2026-08-17\n" +
		"total_files: 3\ntotal_bytes: 1150\n"
	out := h.ok("gc", "--older-than", "30d")
	if !strings.HasPrefix(out, want) || !strings.Contains(out, "--yes") {
		t.Fatalf("gc = %q, want prefix %q", out, want)
	}
	for _, rel := range []string{"reports/t1/a.md", "reports/t1/sub/b.md", "reports/t2/c.md", "shots/big.png", "memory/old.md", "briefs/old.md"} {
		if !homeHas(t, h, rel) {
			t.Fatalf("a dry run deleted %s", rel)
		}
	}
}

func TestGCYesDeletesExactlyTheListedFiles(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	fleetFile(t, h, "reports/t1/a.md", 100, 40*day)
	fleetFile(t, h, "reports/t1/sub/b.md", 50, 35*day)
	fleetFile(t, h, "reports/t1/new.md", 5, day)
	fleetFile(t, h, "shots/big.png", 1000, 31*day)
	fleetFile(t, h, "memory/old.md", 7, 400*day)
	fleetFile(t, h, "briefs/old.md", 7, 400*day)
	out := h.ok("gc", "--older-than", "30d", "--yes")
	if !strings.Contains(out, "applied: true\n") || !strings.Contains(out, "total_files: 3\ntotal_bytes: 1150\n") {
		t.Fatalf("gc --yes = %q", out)
	}
	for rel, want := range map[string]bool{"reports/t1/a.md": false, "reports/t1/sub": false, "shots/big.png": false, "reports/t1/new.md": true, "reports": true, "shots": true, "memory/old.md": true, "briefs/old.md": true} {
		if got := homeHas(t, h, rel); got != want {
			t.Fatalf("%s exists = %v, want %v", rel, got, want)
		}
	}
	has(t, "orient", h.ok("orient"), ",gc.removed,")
	if out := h.ok("gc", "--older-than", "30d", "--yes"); !strings.Contains(out, "total_files: 0\n") {
		t.Fatalf("second gc = %q", out)
	}
}

func TestGCScopeLimitsTheDeletion(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	fleetFile(t, h, "reports/t1/a.md", 100, 40*day)
	fleetFile(t, h, "reports/t10/a.md", 100, 40*day)
	fleetFile(t, h, "shots/t1/a.png", 100, 40*day)
	out := h.ok("gc", "--older-than", "30d", "--yes", "reports/t1", "shots/t1/")
	if !strings.Contains(out, "total_files: 2\n") {
		t.Fatalf("gc = %q", out)
	}
	for rel, want := range map[string]bool{"reports/t1/a.md": false, "shots/t1/a.png": false, "reports/t10/a.md": true} {
		if got := homeHas(t, h, rel); got != want {
			t.Fatalf("%s exists = %v, want %v", rel, got, want)
		}
	}
}

func TestGCRefusesWhatItMayNotTouch(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	fleetFile(t, h, "memory/old.md", 7, 400*day)
	for args, want := range map[string]string{
		"gc":                         "usage: hand gc --older-than DURATION",
		"gc --older-than 12h":        `--older-than "12h" is not a number of days`,
		"gc --older-than 30d memory": `scope "memory" is not inside reports or shots`,
		"gc --older-than 30d reports/../memory/old.md": `is not inside reports or shots`,
		"gc --older-than 30d reports --yes":            "usage: hand gc --older-than DURATION",
		"gc --older-than 30d briefs --yes":             `is not inside reports or shots`,
	} {
		if _, errOut, code := h.run(strings.Fields(args)...); code != 2 || !strings.Contains(errOut, want) {
			t.Fatalf("hand %s: code=%d stderr=%q, want %q", args, code, errOut, want)
		}
	}
	if !homeHas(t, h, "memory/old.md") {
		t.Fatal("memory lost")
	}
}
