package cli_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	for _, args := range [][]string{{"--home", cp, "task", "list"}, {"init", cp}} {
		if _, errOut, code := h.run(args...); code != 3 || !strings.Contains(errOut, "also at "+h.home) {
			t.Fatalf("%q: code=%d stderr=%q", args, code, errOut)
		}
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
