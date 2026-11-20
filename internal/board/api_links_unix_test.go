//go:build unix

package board_test

import (
	"path/filepath"
	"testing"
)

func TestAPILinksFollowsOnlySymlinksThatStayInTheFleet(t *testing.T) {
	outside := fleetFolder(t, map[string]string{"secrets/id_ed25519": secret, "notes.md": secret})
	h, home := apiDocBoard(t, linksFiles())
	t1 := filepath.Join(home, "reports", "t1")
	link(t, filepath.Join(outside, "secrets"), filepath.Join(t1, "escape"))
	link(t, filepath.Join(outside, "notes.md"), filepath.Join(t1, "out.md"))
	link(t, "../../..", filepath.Join(t1, "up"))
	link(t, filepath.Join(outside, "gone"), filepath.Join(t1, "dangling"))
	link(t, "../../hand.db", filepath.Join(t1, "db-link"))
	link(t, "../../projects", filepath.Join(t1, "clones"))
	link(t, "notes.txt", filepath.Join(t1, "notes-link.txt"))
	link(t, "sub", filepath.Join(t1, "sub-link"))
	hardLink(t, filepath.Join(t1, "flow.png"), filepath.Join(t1, "flow2.png"))
	hardLink(t, filepath.Join(home, "hand.db"), filepath.Join(t1, "db.txt"))
	paths := []string{
		"reports/t1/escape", "reports/t1/escape/id_ed25519", "reports/t1/out.md", "reports/t1/up", "reports/t1/dangling", "reports/t1/db-link", "reports/t1/clones", "reports/t1/clones/app/README.md",
		"reports/t1/flow.png", "reports/t1/flow2.png", "reports/t1/db.txt",
		"reports/t1/notes-link.txt", "reports/t1/sub-link", "reports/t1/sub-link/x.txt", "reports/t1/notes.txt",
	}
	got := linksOf(t, h, paths...)
	wantRel := map[string]string{
		"reports/t1/notes-link.txt": "reports/t1/notes.txt",
		"reports/t1/sub-link":       "reports/t1/sub",
		"reports/t1/sub-link/x.txt": "reports/t1/sub/x.txt",
		"reports/t1/notes.txt":      "reports/t1/notes.txt",
	}
	for i, p := range paths {
		want, ok := wantRel[p]
		switch l := got.Links[i]; {
		case ok && (l == nil || l.Rel != want):
			t.Errorf("%q = %+v, want rel %q", p, l, want)
		case !ok && l != nil:
			t.Errorf("%q = %+v, want null", p, *l)
		}
	}
	linksParity(t, h, home, paths)
}
