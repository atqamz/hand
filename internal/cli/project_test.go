package cli_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	var want strings.Builder
	want.WriteString("notes: |\n")
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if line == "" {
			want.WriteString("\n")
			continue
		}
		want.WriteString("  " + line + "\n")
	}
	if out := h.ok("project", "notes", "app"); out != want.String() {
		t.Fatalf("notes = %q, want %q", out, want.String())
	}
}

func TestProjectNotesSaysNoNotesForAKnownProjectWithoutAFile(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	h.ok("project", "add", "app", t.TempDir())
	if out := h.ok("project", "notes", "app"); out != "notes: no notes\n" {
		t.Fatalf("notes = %q, want notes: no notes", out)
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
	for _, text := range []string{"", "   ", "\n\t\r\n"} {
		_, errOut, code := h.run("project", "note", "app", text)
		if code != 2 || !strings.Contains(errOut, "empty") {
			t.Fatalf("text %q: code=%d stderr=%q", text, code, errOut)
		}
	}
	if _, err := os.Stat(filepath.Join(h.home, "notes", "app.md")); !os.IsNotExist(err) {
		t.Fatalf("a refused note wrote the file: %v", err)
	}
}

func TestProjectNoteRefusesUnsafeProjectNames(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	h.ok("project", "add", "app", t.TempDir())
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "app:x", "con", "Com1", "App", "has\nnewline", "\x1b[31mred"} {
		importExec(t, h.home, `INSERT INTO project(name, repo, settings, created_at) VALUES (?, '', '', '2026-09-26T00:00:00Z')`, name)
		for _, cmd := range []string{"note", "notes"} {
			args := []string{"project", cmd, name}
			if cmd == "note" {
				args = append(args, "text")
			}
			_, errOut, code := h.run(args...)
			if code != 2 || !strings.Contains(errOut, "is not a plain note name") {
				t.Fatalf("hand %q: code=%d stderr=%q", args, code, errOut)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(h.home, "notes")); !os.IsNotExist(err) {
		t.Fatalf("notes directory exists after refusals: %v", err)
	}
	if got := field(h.ok("project", "note", "app", "the host is cv.example"), "file"); got != "notes/app.md" {
		t.Fatalf("file = %q, want notes/app.md", got)
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

func TestProjectNoteTrimsTrailingNewlines(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	h.ok("project", "add", "app", t.TempDir())
	date := h.clock().Format("2006-01-02")
	h.ok("project", "note", "app", "first line\r\n\r\nlast line\r\n\n")
	b, err := os.ReadFile(filepath.Join(h.home, "notes", "app.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "# app — notes\n\n## " + date + "\n\nfirst line\r\n\r\nlast line\n\n"
	if string(b) != want {
		t.Fatalf("file = %q, want %q", b, want)
	}
}

func TestProjectNotesStripsControlCharactersFromItsOutput(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	h.ok("project", "add", "app", t.TempDir())
	text := "before \x1b[31mred\x1b[0m \x07 done"
	h.ok("project", "note", "app", text)
	b, err := os.ReadFile(filepath.Join(h.home, "notes", "app.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), text) {
		t.Fatalf("the file does not keep the raw bytes: %q", b)
	}
	out := h.ok("project", "notes", "app")
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Fatalf("notes kept control characters: %q", out)
	}
	if !strings.Contains(out, "before red  done") {
		t.Fatalf("notes = %q, want the control characters stripped", out)
	}
}

func TestProjectNoteConcurrentFirstWritesKeepOneTitle(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	h.ok("project", "add", "app", t.TempDir())
	date := h.clock().Format("2006-01-02")
	const n = 6
	var wg sync.WaitGroup
	fail := make(chan string, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errOut, code := h.run("project", "note", "app", fmt.Sprintf("entry-%d", i))
			if code != 0 {
				fail <- fmt.Sprintf("entry-%d: exit %d: %s", i, code, errOut)
			}
		}(i)
	}
	wg.Wait()
	close(fail)
	for msg := range fail {
		t.Fatal(msg)
	}
	b, err := os.ReadFile(filepath.Join(h.home, "notes", "app.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(b), "# app — notes"); got != 1 {
		t.Fatalf("titles = %d, want 1 in %q", got, b)
	}
	if got := strings.Count(string(b), "## "+date+"\n\n"); got != n {
		t.Fatalf("entries = %d, want %d in %q", got, n, b)
	}
	for i := range n {
		if want := fmt.Sprintf("entry-%d\n\n", i); !strings.Contains(string(b), want) {
			t.Fatalf("entry-%d lost from %q", i, b)
		}
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
