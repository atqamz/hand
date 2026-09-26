package cli_test

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
	_ "modernc.org/sqlite"
)

func TestCommandsFindTheHomeFromAFolderInsideIt(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	h.ok("project", "add", "app", gitRepo(t))
	sub := filepath.Join(h.home, "projects", "x", "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	h.vars["HAND_HOME"] = ""
	h.cwd = sub
	if out := h.ok("project", "list"); !strings.Contains(out, "app,") {
		t.Fatalf("project list from %s = %q", sub, out)
	}
	h.cwd = t.TempDir()
	if _, errOut, code := h.run("project", "list"); code != 3 || !strings.Contains(errOut, "not inside a fleet home") {
		t.Fatalf("outside every home: code=%d stderr=%q", code, errOut)
	}
}

func TestHandHomeAndTheWorkingDirectoryMustAgree(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	other := t.TempDir()
	h.ok("init", other)
	h.cwd = other
	_, errOut, code := h.run("project", "list")
	if code != 3 || !strings.Contains(errOut, "HAND_HOME is "+h.home) || !strings.Contains(errOut, "inside "+other) {
		t.Fatalf("conflict: code=%d stderr=%q", code, errOut)
	}
	if out := h.ok("--home", other, "project", "list"); !strings.Contains(out, "projects[0]") {
		t.Fatalf("explicit --home = %q", out)
	}
}

func TestInitTargetsItsFolderNotHandHome(t *testing.T) {
	h := newHarness(t)
	first := field(h.ok("init"), "id")
	dir := t.TempDir()
	h.cwd = dir
	out := h.ok("init")
	if field(out, "home") != dir || field(out, "id") == first || field(out, "created") != "true" {
		t.Fatalf("init in %s = %q", dir, out)
	}
}

func TestInitNamesAndRenamesTheFleet(t *testing.T) {
	h := newHarness(t)
	out := h.ok("init")
	id := field(out, "id")
	if field(out, "fleet") != filepath.Base(h.home) || !strings.HasPrefix(id, "f") || len(id) != 13 {
		t.Fatalf("first init = %q", out)
	}
	out = h.ok("init", "--name", "Yes 2 Games")
	if field(out, "fleet") != "Yes 2 Games" || field(out, "id") != id || field(out, "created") != "false" {
		t.Fatalf("rename = %q", out)
	}
	dir := t.TempDir()
	if _, _, code := h.run("init", "--name", "bad\nname", dir); code != 2 {
		t.Fatalf("bad name code = %d, want 2", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "hand.db")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a refused name still created a database: %v", err)
	}
}

func TestAMovedHomeIsRefusedUntilInitAdoptsIt(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	old := h.home
	h.home = filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(old, h.home); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := h.run("task", "list"); code != 3 || !strings.Contains(errOut, "moved here from "+old) {
		t.Fatalf("moved home: code=%d stderr=%q", code, errOut)
	}
	if out := h.ok("init"); field(out, "moved_from") != old || !strings.Contains(out, "regenerate") {
		t.Fatalf("init after move = %q", out)
	}
	h.ok("task", "list")
}

func TestACopiedHomeIsRefused(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	cp := t.TempDir()
	db, err := os.ReadFile(filepath.Join(h.home, "hand.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cp, "hand.db"), db, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--home", cp, "task", "list"}, {"init", cp}, {"init", "--name", "renamed", cp}} {
		if _, errOut, code := h.run(args...); code != 3 || !strings.Contains(errOut, "also at "+h.home) {
			t.Fatalf("%q: code=%d stderr=%q", args, code, errOut)
		}
	}
	for _, f := range []string{"AGENTS.md", "memory", "routing.json"} {
		if _, err := os.Stat(filepath.Join(cp, f)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("a refused copy was changed: %s: %v", f, err)
		}
	}
	st, err := state.Open(filepath.Join(cp, "hand.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if f, err := st.Fleet(context.Background()); err != nil || f.Name != filepath.Base(h.home) {
		t.Fatalf("a refused copy was renamed: %+v, %v", f, err)
	}
}

func TestInitRefusesAnUnusableFolderNameBeforeWriting(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(t.TempDir(), strings.Repeat("x", 65))
	if _, errOut, code := h.run("init", dir); code != 2 || !strings.Contains(errOut, "pass --name") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a refused init wrote %d entries", len(entries))
	}
	if out := h.ok("init", "--name", "long", dir); field(out, "fleet") != "long" {
		t.Fatalf("init --name = %q", out)
	}
}

func TestASymlinkedHomeIsTheSameFleet(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(h.home, alias); err != nil {
		t.Fatal(err)
	}
	h.vars["HAND_HOME"] = alias
	h.cwd = alias
	if _, errOut, code := h.run("task", "list"); code != 0 {
		t.Fatalf("through a symlink: code=%d stderr=%q", code, errOut)
	}
}

func TestAMissingHomeCreatesNothing(t *testing.T) {
	h := newHarness(t)
	h.home = filepath.Join(t.TempDir(), "nope")
	h.cwd = t.TempDir()
	brief := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(brief, []byte("fix it"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"task", "list"}, {"route", "list"}, {"attempt", "start", "--profile", "default", "--prompt-file", brief, "t1"}} {
		if _, errOut, code := h.run(args...); code != 3 || !strings.Contains(errOut, "run `hand init`") {
			t.Fatalf("%q: code=%d stderr=%q", args, code, errOut)
		}
	}
	if _, err := os.Stat(h.home); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a missing home was created: %v", err)
	}
}

func TestInitRefusesToNestAFleet(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	outer := t.TempDir()
	cases := map[string]struct{ root, dir, want string }{
		"holds the shared folder":  {filepath.Join(outer, ".secondhand"), outer, "holds Hand's shared folder"},
		"inside the shared folder": {h.vars["SECONDHAND_HOME"], filepath.Join(h.vars["SECONDHAND_HOME"], "worktrees", "x"), "inside Hand's shared folder"},
		"inside another fleet":     {h.vars["SECONDHAND_HOME"], filepath.Join(h.home, "projects", "app"), "inside the fleet at " + h.home},
	}
	for name, c := range cases {
		h.vars["SECONDHAND_HOME"] = c.root
		if _, errOut, code := h.run("init", c.dir); code != 3 || !strings.Contains(errOut, c.want) {
			t.Fatalf("%s: code=%d stderr=%q", name, code, errOut)
		}
		for _, f := range []string{"hand.db", "AGENTS.md"} {
			if _, err := os.Stat(filepath.Join(c.dir, f)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("%s: refused init wrote %s: %v", name, f, err)
			}
		}
	}
}

func TestInitSeesThroughASymlinkedParent(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(h.home, alias); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(alias, "new")
	if _, errOut, code := h.run("init", target); code != 3 || !strings.Contains(errOut, "inside the fleet at "+h.home) {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(h.home, "new")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("init created a nested fleet through the symlink: %v", err)
	}
	shared := filepath.Join(t.TempDir(), "shared")
	if err := os.Symlink(h.vars["SECONDHAND_HOME"], shared); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := h.run("init", filepath.Join(shared, "new")); code != 3 || !strings.Contains(errOut, "inside Hand's shared folder") {
		t.Fatalf("through a link into the shared folder: code=%d stderr=%q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(h.vars["SECONDHAND_HOME"], "new")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("init created a fleet inside the shared folder: %v", err)
	}
}

func TestAFailedAdoptionLeavesTheMoveUnadopted(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	old := h.home
	h.home = filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(old, h.home); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.home, "AGENTS.md"), []byte("# someone else's rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, code := h.run("init"); code != 3 {
		t.Fatalf("init with a foreign AGENTS.md: code=%d", code)
	}
	if _, errOut, code := h.run("task", "list"); code != 3 || !strings.Contains(errOut, "moved here from "+old) {
		t.Fatalf("after a failed adoption: code=%d stderr=%q", code, errOut)
	}
}

func TestInitRebasesAnAbsoluteProjectPathFromTheOldHome(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	repo := filepath.Join(h.home, "projects", "app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	h.ok("project", "add", "app", repo)
	db, err := sql.Open("sqlite", filepath.Join(h.home, "hand.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE project SET repo = ? WHERE name = 'app'`, repo); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(h.home, moved); err != nil {
		t.Fatal(err)
	}
	h.home = moved
	h.ok("init")
	if list := h.ok("project", "list"); !strings.Contains(list, "app,"+filepath.Join(moved, "projects", "app")) {
		t.Fatalf("project list = %q", list)
	}
}

func TestA07FleetGetsTheMigrationHint(t *testing.T) {
	h := newHarness(t)
	old := t.TempDir()
	for _, d := range []string{"state", "data", "data/145-ship"} {
		if err := os.MkdirAll(filepath.Join(old, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(old, "state", "hand.db"), []byte("0.7"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.vars["HAND_HOME"] = ""
	h.cwd = filepath.Join(old, "data", "145-ship")
	_, errOut, code := h.run("task", "list")
	if code != 3 || !strings.Contains(errOut, old+" is a Hand 0.7 fleet") || !strings.Contains(errOut, "secondhand-migrate") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}
