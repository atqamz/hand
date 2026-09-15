package toolchain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestGitArgsWithTemplatePrependsInitTemplateDir(t *testing.T) {
	got := GitArgsWithTemplate("/fake/git-templates", []string{"clone", "--no-local", "src", "dst"})
	want := []string{"-c", "init.templateDir=/fake/git-templates", "clone", "--no-local", "src", "dst"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GitArgsWithTemplate() = %v, want %v", got, want)
	}
}

func TestGitArgsWithTemplateLeavesArgsUnchangedWhenNoDirectory(t *testing.T) {
	args := []string{"init", "--initial-branch=main", "path"}
	got := GitArgsWithTemplate("", args)
	if !reflect.DeepEqual(got, args) {
		t.Fatalf("GitArgsWithTemplate() = %v, want %v unchanged", got, args)
	}
}

func TestGitTransportAvailableSearchesManagedAndInheritedPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows helper executability is determined by the .exe suffix")
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	reportedPath := t.TempDir()
	managedPath := t.TempDir()
	inheritedPath := t.TempDir()
	for _, path := range []string{
		filepath.Join(reportedPath, "git-remote-https"),
		filepath.Join(managedPath, "git-remote-https"),
	} {
		if err := os.WriteFile(path, []byte("not executable"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(inheritedPath, "git-remote-https"), []byte("executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_EXEC_PATH", reportedPath)
	t.Setenv("PATH", inheritedPath)

	available, err := (Runtime{GitPath: gitPath, GitBin: managedPath}).GitTransportAvailable(context.Background(), "https")
	if err != nil {
		t.Fatal(err)
	}
	if !available {
		t.Fatal("GitTransportAvailable = false, want executable helper later on managed PATH")
	}
}

func TestGitTransportAvailableReturnsErrorForUnknownManagedPathEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows reports a file-as-directory lookup as path-not-found")
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	reportedPath := t.TempDir()
	managedPath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(managedPath, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_EXEC_PATH", reportedPath)
	t.Setenv("PATH", t.TempDir())

	if _, err := (Runtime{GitPath: gitPath, GitBin: managedPath}).GitTransportAvailable(context.Background(), "https"); err == nil {
		t.Fatal("GitTransportAvailable accepted an uninspectable managed PATH entry")
	}
}
