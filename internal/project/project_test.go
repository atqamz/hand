package project

import (
	"fmt"
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

func TestListRejectsMalformedLine(t *testing.T) {
	dir := t.TempDir()
	writeRegistry(t, dir, "# Projects\n\nnot a project\n")

	if _, err := List(dir); err == nil {
		t.Fatal("expected malformed registry line to fail")
	}
}

func TestListRejectsUnknownMode(t *testing.T) {
	dir := t.TempDir()
	writeRegistry(t, dir, "- nsr: https://github.com/yes2games/nsr mode=unknown\n")

	if _, err := List(dir); err == nil {
		t.Fatal("expected unknown project mode to fail")
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

func TestAddAppendsAfterRegistryWithoutTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	writeRegistry(t, dir, "- existing: https://github.com/org/existing mode=direct-pr")

	if err := Add(dir, Project{Name: "new", URL: "https://github.com/org/new", Mode: "direct-pr"}); err != nil {
		t.Fatal(err)
	}

	projects, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 || projects[1].Name != "new" {
		t.Fatalf("got %+v", projects)
	}
}

func TestConcurrentAddsPreserveAllProjects(t *testing.T) {
	dir := t.TempDir()
	writeRegistry(t, dir, "# Projects\n")

	const count = 20
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		go func(i int) {
			errs <- Add(dir, Project{Name: fmt.Sprintf("project-%d", i), URL: "https://github.com/org/repo", Mode: "direct-pr"})
		}(i)
	}
	for i := 0; i < count; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}

	projects, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != count {
		t.Fatalf("got %d projects, want %d", len(projects), count)
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

func TestRemoveFailsClosedOnMalformedLine(t *testing.T) {
	dir := t.TempDir()
	content := "# Projects\n\n- nsr: https://github.com/yes2games/nsr mode=direct-pr\n\ncustom note\n- other: https://github.com/org/other mode=local-only\n"
	writeRegistry(t, dir, content)

	if err := Remove(dir, "nsr"); err == nil {
		t.Fatal("expected unrecognized line to fail closed")
	}
	if got, err := os.ReadFile(RegistryPath(dir)); err != nil || string(got) != content {
		t.Fatalf("registry changed after failed remove: %q, %v", got, err)
	}
}
