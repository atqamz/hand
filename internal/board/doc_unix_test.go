//go:build unix

package board_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

func link(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Symlink(target, at); err != nil {
		t.Fatal(err)
	}
}

func TestASymlinkOutOfTheFleetIsRefused(t *testing.T) {
	outside := fleetFolder(t, map[string]string{"secrets/id_ed25519": secret, "notes.md": secret})
	h, home := viewer(t, map[string]string{"reports/t1/design.md": design})
	link(t, filepath.Join(outside, "secrets"), filepath.Join(home, "reports", "t1", "escape"))
	link(t, filepath.Join(outside, "notes.md"), filepath.Join(home, "reports", "t1", "notes.md"))
	link(t, "../../..", filepath.Join(home, "reports", "t1", "up"))
	for _, path := range []string{"/doc/reports/t1/escape", "/doc/reports/t1/escape/id_ed25519", "/doc/reports/t1/notes.md", "/doc/reports/t1/notes.md?download", "/doc/reports/t1/up"} {
		refused(t, h, path, policy["outside"])
		if body := request(h, "GET", path, nil, true).Body.String(); strings.Contains(body, outside) || strings.Contains(body, "secrets") {
			t.Errorf("%s names the link's target:\n%s", path, body)
		}
	}
	link(t, filepath.Join(outside, "gone"), filepath.Join(home, "reports", "t1", "dangling"))
	if rec := request(h, "GET", "/doc/reports/t1/dangling", nil, true); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), outside) {
		t.Errorf("dangling = %d:\n%s", rec.Code, rec.Body)
	}
}

func TestASymlinkToHandsDatabaseIsRefused(t *testing.T) {
	h, home := viewer(t, map[string]string{"hand.db": secret, "board.token": secret, "projects/app/README.md": secret, "reports/t0/notes.md": "# notes\n"})
	link(t, "../hand.db", filepath.Join(home, "reports", "db-link"))
	link(t, filepath.Join(home, "board.token"), filepath.Join(home, "reports", "token"))
	link(t, "../projects", filepath.Join(home, "reports", "clones"))
	link(t, "..", filepath.Join(home, "reports", "root"))
	link(t, "t0/notes.md", filepath.Join(home, "reports", "notes-link.md"))
	for _, path := range []string{"/doc/reports/db-link", "/doc/reports/token", "/doc/reports/clones/app/README.md", "/doc/reports/clones", "/doc/reports/root", "/doc/reports/root/hand.db"} {
		refused(t, h, path, policy["hidden"])
	}
	contains(t, "a link inside the fleet", get(t, h, "/doc/reports/notes-link.md"), "<h3>notes</h3>", `<h2 class="thread-title">notes-link.md</h2>`)
	contains(t, "listing", get(t, h, "/doc/reports"), `<span class="badge" aria-hidden="true">LINK</span><span class="name">db-link</span>`)
}

func TestAFifoIsRefused(t *testing.T) {
	h, home := viewer(t, map[string]string{"reports/t1/design.md": design})
	if err := syscall.Mkfifo(filepath.Join(home, "reports", "t1", "pipe"), 0o644); err != nil {
		t.Skip("no FIFO here:", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		refused(t, h, "/doc/reports/t1/pipe", policy["kind"])
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a FIFO blocked the viewer")
	}
	if _, _, _, err := board.Resolve(home, "reports/t1/pipe"); err == nil {
		t.Fatal("Resolve allowed a FIFO")
	}
}

func TestAFleetFolderBehindASymlinkStillLinks(t *testing.T) {
	real, err := filepath.EvalSymlinks(fleetFolder(t, map[string]string{"reports/t1/design.md": design}))
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "fleet")
	link(t, real, home)
	st := open(t)
	task := active(t, st, "Design")
	a := attempt(t, st, task.ID, state.AttemptRunning, "")
	if _, err := st.AddReport(context.Background(), a.ID, state.ReportDone, "Delivered: "+filepath.Join(real, "reports", "t1", "design.md")); err != nil {
		t.Fatal(err)
	}
	h := board.New(st, token, board.Options{Home: home})
	contains(t, "task page", get(t, h, "/task/t1"), `<a class="file" href="/doc/reports/t1/design.md"`)
	contains(t, "viewer", get(t, h, "/doc/reports/t1/design.md"), "<h3>Export design</h3>")
}

func TestASymlinkOutOfTheFleetStaysText(t *testing.T) {
	outside := fleetFolder(t, map[string]string{"secrets/id_ed25519": secret})
	home := fleetFolder(t, pathFiles)
	link(t, filepath.Join(outside, "secrets"), filepath.Join(home, "reports", "t1", "escape"))
	link(t, "../../hand.db", filepath.Join(home, "reports", "t1", "db-link"))
	link(t, "../../projects", filepath.Join(home, "reports", "t1", "clones"))
	staysText(t, home, filepath.Join(home, "reports", "t1", "escape", "id_ed25519"), filepath.Join(home, "reports", "t1", "escape"), filepath.Join(home, "reports", "t1", "db-link"), filepath.Join(home, "reports", "t1", "clones", "app", "README.md"))
}
