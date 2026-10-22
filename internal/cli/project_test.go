package cli_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

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
