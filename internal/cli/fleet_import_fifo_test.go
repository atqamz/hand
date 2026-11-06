//go:build !windows

package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func runImportWithin(t *testing.T, h *harness, args ...string) (string, string, int) {
	t.Helper()
	type result struct {
		out, errOut string
		code        int
	}
	done := make(chan result, 1)
	go func() {
		out, errOut, code := h.run(args...)
		done <- result{out, errOut, code}
	}()
	select {
	case r := <-done:
		return r.out, r.errOut, r.code
	case <-time.After(20 * time.Second):
		t.Fatal("the import blocked on a FIFO destination")
		return "", "", 0
	}
}

func TestFleetImportTreatsANonregularDestinationAsACollision(t *testing.T) {
	fx := importFixture(t)
	fx.h.ok("--home", fx.src, "project", "add", "app", gitRepo(t))
	fx.h.ok("--home", fx.src, "task", "add", "app", "one")
	writeImportFile(t, fx.src, "memory/projects/app.md", "lesson\n")
	importExec(t, fx.src, `INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, created_at) VALUES(1, 1, 'claude', 'sonnet', 'low', 'x', 'w', 'b', 'exited', '2026-10-06T00:00:00Z')`)
	importExec(t, fx.src, `INSERT INTO report(id, attempt_id, task_id, status, body, created_at) VALUES(1, 1, 1, 'done', 'the body', '2026-10-06T00:01:00Z')`)
	lesson := filepath.Join(fx.dest, "memory", "projects", "app.md")
	if err := syscall.Mkfifo(lesson, 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(fx.dest, "reports", "archive-"+fx.srcID)
	if err := os.MkdirAll(archive, 0o700); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(archive, "r1-one.md")
	if err := syscall.Mkfifo(report, 0o600); err != nil {
		t.Fatal(err)
	}
	wants := []string{
		"memory/projects/app.md,memory/projects/app.md,destination is not a regular file",
		"r1,reports/archive-" + fx.srcID + "/r1-one.md,destination is not a regular file",
	}
	for _, args := range [][]string{
		{"fleet", "import", "--dry-run", fx.srcID, "INTO", fx.destID},
		{"fleet", "import", fx.srcID, "INTO", fx.destID},
	} {
		out, errOut, code := runImportWithin(t, fx.h, args...)
		if code != 0 {
			t.Fatalf("%v: code=%d out=%q stderr=%q", args, code, out, errOut)
		}
		for _, want := range wants {
			if !strings.Contains(out, want) {
				t.Fatalf("%v: manifest lacks %q:\n%s", args, want, out)
			}
		}
	}
	for _, p := range []string{lesson, report} {
		fi, err := os.Stat(p)
		if err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
			t.Fatalf("the import replaced %s: %v", p, err)
		}
	}
}
