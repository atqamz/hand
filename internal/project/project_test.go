package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeriveName(t *testing.T) {
	cases := map[string]string{
		"https://github.com/org/repo":     "repo",
		"https://github.com/org/repo.git": "repo",
		"git@github.com:org/repo.git":     "repo",
		"local":                           "local",
	}
	for url, want := range cases {
		if got := DeriveName(url); got != want {
			t.Errorf("DeriveName(%q) = %q, want %q", url, got, want)
		}
	}
}

func writeRegistry(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(RegistryPath(dir), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListParsesRegistry(t *testing.T) {
	dir := t.TempDir()
	writeRegistry(t, dir, "# Projects\n\n- nsr: https://github.com/yes2games/nsr mode=direct-pr\n- secondhand: local mode=local-only\n")

	projects, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 {
		t.Fatalf("got %d projects, want 2", len(projects))
	}
	if projects[0] != (Project{Name: "nsr", URL: "https://github.com/yes2games/nsr", Mode: "direct-pr"}) {
		t.Errorf("got %+v", projects[0])
	}
	if projects[1] != (Project{Name: "secondhand", URL: "local", Mode: "local-only"}) {
		t.Errorf("got %+v", projects[1])
	}
}

func TestListMissingFile(t *testing.T) {
	dir := t.TempDir()
	projects, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if projects != nil {
		t.Errorf("got %+v, want nil", projects)
	}
}

func TestAddDuplicateRejected(t *testing.T) {
	dir := t.TempDir()
	writeRegistry(t, dir, "# Projects\n\n- nsr: https://github.com/yes2games/nsr mode=direct-pr\n")

	err := Add(dir, Project{Name: "nsr", URL: "https://github.com/other/nsr", Mode: "direct-pr"})
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
}

func TestAddAppends(t *testing.T) {
	dir := t.TempDir()
	writeRegistry(t, dir, "# Projects\n\n")

	if err := Add(dir, Project{Name: "nsr", URL: "https://github.com/yes2games/nsr", Mode: "direct-pr"}); err != nil {
		t.Fatal(err)
	}

	projects, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Name != "nsr" {
		t.Fatalf("got %+v", projects)
	}
}

func TestFind(t *testing.T) {
	dir := t.TempDir()
	writeRegistry(t, dir, "# Projects\n\n- nsr: https://github.com/yes2games/nsr mode=direct-pr\n")

	p, ok, err := Find(dir, "nsr")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected to find nsr")
	}
	if p.URL != "https://github.com/yes2games/nsr" {
		t.Errorf("got %+v", p)
	}

	_, ok, err = Find(dir, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected not to find missing project")
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	writeRegistry(t, dir, "# Projects\n\n- nsr: https://github.com/yes2games/nsr mode=direct-pr\n- other: https://github.com/org/other mode=local-only\n")

	if err := Remove(dir, "nsr"); err != nil {
		t.Fatal(err)
	}

	projects, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Name != "other" {
		t.Fatalf("got %+v", projects)
	}
}

func TestRemoveNotFound(t *testing.T) {
	dir := t.TempDir()
	writeRegistry(t, dir, "# Projects\n\n")

	if err := Remove(dir, "missing"); err == nil {
		t.Fatal("expected error for missing project")
	}
}
