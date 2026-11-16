//go:build unix

package board_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/atqamz/hand/internal/board"
)

func TestAPIDocRefusesLinksAndSpecialFilesLikeTheBoard(t *testing.T) {
	outside := fleetFolder(t, map[string]string{"secrets/id_ed25519": secret, "notes.md": secret})
	h, home := apiDocBoard(t, map[string]string{"hand.db": secret, "board.token": secret, "projects/app/README.md": secret, "reports/t0/notes.md": "# notes\n", "reports/t1/design.md": design})
	t1 := filepath.Join(home, "reports", "t1")
	link(t, filepath.Join(outside, "secrets"), filepath.Join(t1, "escape"))
	link(t, filepath.Join(outside, "notes.md"), filepath.Join(t1, "outside.md"))
	link(t, "../../..", filepath.Join(t1, "up"))
	link(t, filepath.Join(outside, "gone"), filepath.Join(t1, "dangling"))
	link(t, "../../hand.db", filepath.Join(t1, "db-link"))
	link(t, filepath.Join(home, "board.token"), filepath.Join(t1, "token"))
	link(t, "../../projects", filepath.Join(t1, "clones"))
	link(t, "../..", filepath.Join(t1, "root"))
	link(t, "../t0/notes.md", filepath.Join(t1, "notes-link.md"))
	if err := syscall.Mkfifo(filepath.Join(t1, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	sameAsTheBoard(t, h, home, []probe{
		{"reports/t1/escape", 403}, {"reports/t1/escape/id_ed25519", 403}, {"reports/t1/outside.md", 403}, {"reports/t1/up", 403},
		{"reports/t1/dangling", 404}, {"reports/t1/db-link", 403}, {"reports/t1/token", 403}, {"reports/t1/clones", 403},
		{"reports/t1/clones/app/README.md", 403}, {"reports/t1/root", 403}, {"reports/t1/root/hand.db", 403}, {"reports/t1/pipe", 403},
		{"reports/t1/notes-link.md", 200}, {"reports/t1", 200},
	})
	_, link := apiInto[board.APIDocFile](t, h, "/api/v1/doc/reports/t1/notes-link.md")
	if link.Kind != "markdown" || link.Path != "reports/t1/notes-link.md" {
		t.Errorf("a link inside the fleet = %+v", link)
	}
	_, folder := apiInto[board.APIDocFolder](t, h, "/api/v1/doc/reports/t1")
	refusedBy := map[string]string{}
	for _, e := range folder.Entries {
		if e.Refused != "" {
			refusedBy[e.Name] = e.Refused
			if e.Kind != "link" || e.Label != "LINK" || e.Size != nil {
				t.Errorf("refused entry %+v", e)
			}
		}
		if e.Name == "notes-link.md" && (e.Kind != "link" || e.Refused != "") {
			t.Errorf("a link inside the fleet = %+v", e)
		}
	}
	for name, why := range map[string]string{"escape": "leads outside the fleet folder", "dangling": "points at nothing", "db-link": "points at a file the Board never shows", "clones": "points at a file the Board never shows"} {
		if refusedBy[name] != why {
			t.Errorf("%s refused as %q, want %q", name, refusedBy[name], why)
		}
	}
	page := get(t, h, "/doc/reports/t1")
	for name, why := range refusedBy {
		contains(t, name, page, `<span class="name">`+name+`</span><span class="meta">`+why+`</span>`)
	}
	rec := request(h, "GET", "/api/v1/doc/reports/t1", nil, true)
	if strings.Contains(rec.Body.String(), outside) || strings.Contains(rec.Body.String(), "secrets") {
		t.Errorf("the listing names a link's target:\n%s", rec.Body)
	}
}

func TestAPIDocNeighboursSkipWhatTheBoardWillNotShow(t *testing.T) {
	outside := fleetFolder(t, map[string]string{"x.txt": secret})
	h, home := apiDocBoard(t, map[string]string{"reports/n/a.txt": "a", "reports/n/c.txt": "c", "reports/n/e.txt": "e"})
	link(t, filepath.Join(outside, "x.txt"), filepath.Join(home, "reports", "n", "b.txt"))
	link(t, filepath.Join(outside, "gone"), filepath.Join(home, "reports", "n", "d.txt"))
	_, got := apiInto[board.APIDocFile](t, h, "/api/v1/doc/reports/n/c.txt")
	if got.Prev == nil || got.Prev.Name != "a.txt" || got.Next == nil || got.Next.Name != "e.txt" {
		t.Fatalf("c.txt neighbours = %+v %+v", got.Prev, got.Next)
	}
}

func TestAPIDocRefusesAnUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every file")
	}
	h, home := apiDocBoard(t, map[string]string{"reports/t1/locked.txt": secret})
	if err := os.Chmod(filepath.Join(home, "reports", "t1", "locked.txt"), 0); err != nil {
		t.Fatal(err)
	}
	sameAsTheBoard(t, h, home, []probe{{"reports/t1/locked.txt", 403}})
	e := testAPIError(t, request(h, "GET", "/api/v1/doc/reports/t1/locked.txt", nil, true), http.StatusForbidden, "forbidden")
	if e.Message != "the Board cannot read this path" {
		t.Errorf("message = %q", e.Message)
	}
}
