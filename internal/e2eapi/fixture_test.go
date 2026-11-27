package e2eapi_test

import (
	"os"
	"path/filepath"
	"testing"
)

type made struct {
	id   string
	init toonDoc
}

func fixture(t *testing.T, b *box) made {
	t.Helper()
	alpha := b.repo(t, "alpha")
	beta := b.repo(t, "beta")
	seeded := seedLuvus(t, b, luvusTested)
	msg := "hand init downloads the pinned Luvus"
	if seeded {
		msg = "hand init with the cached Luvus"
	}
	init := toon(initialize(t, b, msg).out)
	saveLuvus(t, b)
	docs := filepath.Join(b.fleet, "docs")
	if err := os.MkdirAll(filepath.Join(docs, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(docs, "export-notes.md"), "# Export notes\n\nThe export writes **one row per task**.\n\n| column | meaning |\n|---|---|\n| id | task ref |\n| title | task title |\n")
	write(t, filepath.Join(docs, "nested", "log.txt"), "line one\nline two\n")
	for _, hidden := range []string{"luvus", "locks", "projects", "backups", "cache"} {
		if err := os.MkdirAll(filepath.Join(b.fleet, hidden), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(b.fleet, hidden, "note.txt"), "inside "+hidden+"\n")
	}
	ok(t, b.hand(t, "project", "add", "alpha", alpha), "hand project add alpha")
	ok(t, b.hand(t, "project", "add", "beta", beta), "hand project add beta")
	ok(t, b.hand(t, "task", "add", "--goal", "People land on the home page after signing in.", "alpha", "Fix the login redirect"), "hand task add t1")
	ok(t, b.hand(t, "task", "add", "--goal", "Let an operator download every task as CSV.", "beta", "Add a CSV export"), "hand task add t2")
	ok(t, b.hand(t, "task", "add", "--goal", "Describe the install steps.", "alpha", "Update the README"), "hand task add t3")
	ok(t, b.hand(t, "task", "start", "t2"), "hand task start t2")
	ok(t, b.hand(t, "plan", "set", "--body", "1. Add the endpoint.\n2. Add the button.\n", "t2"), "hand plan set t2")
	ok(t, b.handWith(t, handOpts{input: "Which delimiter should the CSV export use?\n\n1. Comma — what most tools expect\n2. Semicolon — what spreadsheet locales in Europe expect\n3. Other\n"}, "decision", "ask", "--file", "-", "t2"), "hand decision ask t2")
	ok(t, b.hand(t, "task", "deliver", "t2", filepath.Join(docs, "export-notes.md")), "hand task deliver t2")
	return made{id: init.fields["id"], init: init}
}

func write(t *testing.T, p, text string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
