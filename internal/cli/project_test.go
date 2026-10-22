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
