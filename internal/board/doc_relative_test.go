package board_test

import (
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/markdown"
)

func TestARelativePathInsideTheFleetLinksToTheViewer(t *testing.T) {
	home := fleetFolder(t, map[string]string{"reports/t1/design.md": design, "reports/t1/assets/a.png": string(flowPNG)})
	for rel, want := range map[string]string{
		"reports/t1/design.md":       "/doc/reports/t1/design.md",
		"./reports/t1/design.md":     "/doc/reports/t1/design.md",
		"reports/t1/assets/":         "/doc/reports/t1/assets",
		"reports/t1/assets":          "/doc/reports/t1/assets",
		"reports/t1/missing.md":      "",
		"reports/t1/assets/b.png":    "",
		"reports/t1/design.md/x":     "",
		"reports/t1/../t1/design.md": "",
		"reports/..":                 "",
		"../" + "elsewhere.md":       "",
	} {
		if got := board.DocHref(home, "", rel); got != want {
			t.Errorf("DocHref(%q) = %q, want %q", rel, got, want)
		}
	}
}

func TestARelativePathInAChatMessageLinksOnlyWhenItExists(t *testing.T) {
	home := fleetFolder(t, map[string]string{"reports/some/file.md": design})
	local := func(p string) string { return board.DocHref(home, "", p) }
	got := string(markdown.Render("read `reports/some/file.md` not `reports/some/gone.md` or reports/some/file.md", local))
	want := `<p>read <a class="path" href="/doc/reports/some/file.md" target="_blank" rel="noopener"><code>reports/some/file.md</code></a> not <code>reports/some/gone.md</code> or reports/some/file.md</p>`
	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

func TestARelativePathToHandsOwnFilesGivesNoLink(t *testing.T) {
	home := fleetFolder(t, map[string]string{
		"hand.db": secret, "board.token": secret, "AGENTS.md": secret,
		".env": secret, "reports/.secret/key": secret, "reports/t1/.draft.md": secret,
		"projects/app/README.md": secret, "luvus/server.pid": secret, "locks/t1.lock": secret,
		"Projects/app/README.md": secret, "LUVUS/x": secret,
		"reports/t1/design.md": design,
	})
	for _, rel := range []string{"hand.db", "board.token", "AGENTS.md", ".env", "reports/.secret/key", "reports/t1/.draft.md", ".git/config", "projects/app/README.md", "projects/app", "luvus/server.pid", "luvus/", "locks/t1.lock", "Projects/app/README.md", "LUVUS/x", "./projects/app/README.md", "reports/../projects/app/README.md"} {
		if got := board.DocHref(home, "", rel); got != "" {
			t.Errorf("DocHref(%q) = %q, want no link", rel, got)
		}
	}
	if board.DocHref(home, "", "reports/t1/design.md") == "" {
		t.Error("the control path is not linked")
	}
}
