package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func TestProjectAddRefusesARepositoryItCannotRead(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	repo := gitRepo(t)
	f, err := os.OpenFile(filepath.Join(repo, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("[[broken\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := h.run("project", "add", "app", repo); code != 1 || !strings.Contains(errOut, "bad config line") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if list := h.ok("project", "list"); strings.Contains(list, "app") {
		t.Fatalf("a refused project was registered: %q", list)
	}
}

func TestProjectNoteCreatesTheFileAndItsFirstEntry(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	h.ok("project", "add", "app", t.TempDir())
	date := h.clock().Format("2006-01-02")
	out := h.ok("project", "note", "app", "the host is cv.example")
	if got := field(out, "file"); got != "notes/app.md" {
		t.Fatalf("file = %q, want notes/app.md", got)
	}
	if got := field(out, "date"); got != date {
		t.Fatalf("date = %q, want %q", got, date)
	}
	dir := filepath.Join(h.home, "notes")
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("notes directory: %v, %v", info, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "app.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "# app — notes\n\n## " + date + "\n\nthe host is cv.example\n\n"
	if string(b) != want {
		t.Fatalf("file = %q, want %q", b, want)
	}
}

func TestProjectNoteAppendsWithoutLosingTheFirstEntry(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	h.ok("project", "add", "app", t.TempDir())
	date := h.clock().Format("2006-01-02")
	h.ok("project", "note", "app", "first")
	h.ok("project", "note", "app", "second")
	b, err := os.ReadFile(filepath.Join(h.home, "notes", "app.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Count(string(b), "## "+date+"\n\n"), 2; got != want {
		t.Fatalf("entries = %d, want %d in %q", got, want, b)
	}
	if !strings.Contains(string(b), "first\n\n") || !strings.Contains(string(b), "second\n\n") {
		t.Fatalf("file lost an entry: %q", b)
	}
	if !strings.HasPrefix(string(b), "# app — notes\n\n## "+date+"\n\nfirst\n\n") {
		t.Fatalf("first entry was not preserved: %q", b)
	}
}

func TestProjectNotesPrintsTheFile(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	h.ok("project", "add", "app", t.TempDir())
	h.ok("project", "note", "app", "one\n\ntwo")
	b, err := os.ReadFile(filepath.Join(h.home, "notes", "app.md"))
	if err != nil {
		t.Fatal(err)
	}
	if out := h.ok("project", "notes", "app"); out != string(b) {
		t.Fatalf("notes = %q, want %q", out, b)
	}
}

func TestProjectNotesSaysNoNotesForAKnownProjectWithoutAFile(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	h.ok("project", "add", "app", t.TempDir())
	if out := h.ok("project", "notes", "app"); out != "no notes\n" {
		t.Fatalf("notes = %q, want no notes", out)
	}
}

func TestProjectNoteRefusesAnUnknownProjectWithTheKnownNames(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	h.ok("project", "add", "app", t.TempDir())
	h.ok("project", "add", "web", t.TempDir())
	_, errOut, code := h.run("project", "note", "nope", "text")
	if code != 3 || !strings.Contains(errOut, "known projects: app, web") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestProjectNoteRefusesEmptyText(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	h.ok("project", "add", "app", t.TempDir())
	_, errOut, code := h.run("project", "note", "app", "")
	if code != 2 || !strings.Contains(errOut, "empty") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestProjectNotePreservesMultiLineText(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	h.ok("project", "add", "app", t.TempDir())
	date := h.clock().Format("2006-01-02")
	text := "port 8080\nlogin at /srv/app\n\nquirks stay here"
	h.ok("project", "note", "app", text)
	b, err := os.ReadFile(filepath.Join(h.home, "notes", "app.md"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "## " + date + "\n\n" + text + "\n\n"; !strings.Contains(string(b), want) {
		t.Fatalf("file = %q, want it to contain %q", b, want)
	}
}

func TestProjectNoteFileLivesInTheFleetHomeWithoutAbsolutePaths(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	h.ok("project", "add", "app", t.TempDir())
	h.ok("project", "note", "app", "host cv.example, path /srv/app, port 8080")
	file := filepath.Join(h.home, "notes", "app.md")
	if rel, err := filepath.Rel(h.home, file); err != nil || rel != filepath.Join("notes", "app.md") {
		t.Fatalf("file %s is not inside the fleet home %s: %q, %v", file, h.home, rel, err)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), h.home) {
		t.Fatalf("the note file holds an absolute fleet path: %q", b)
	}
}

func TestProjectAddRecordsTheSettingsItFinds(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	repo := gitRepo(t)
	gitIn(t, repo, "config", "core.sshCommand", "ssh -i key")
	h.ok("project", "add", "app", repo)
	h.ok("project", "add", "plain", t.TempDir())
	st, err := state.Open(filepath.Join(h.home, "hand.db"), h.clock)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	app, err := st.Project(context.Background(), "app")
	if err != nil || !strings.HasPrefix(app.Settings, "v1\n") || !strings.HasSuffix(app.Settings, " core.sshcommand\n") {
		t.Fatalf("app = %+v, %v", app, err)
	}
	if plain, err := st.Project(context.Background(), "plain"); err != nil || plain.Settings != "" {
		t.Fatalf("a folder that is no repository records nothing: %+v, %v", plain, err)
	}
}
