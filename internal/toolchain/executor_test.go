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

func TestGitTransportAvailablePreservesExecPathWhitespace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX helper paths can end in whitespace and use a shell script")
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		suffix string
	}{
		{name: "space", suffix: " "},
		{name: "tab", suffix: "\t"},
		{name: "newline", suffix: "\n"},
		{name: "carriage return", suffix: "\r"},
		{name: "nonbreaking space", suffix: "\u00a0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reportedPath := filepath.Join(t.TempDir(), "git-core"+test.suffix)
			if err := os.Mkdir(reportedPath, 0o700); err != nil {
				t.Fatal(err)
			}
			helper := filepath.Join(reportedPath, "git-remote-https")
			if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf 'exact-helper-reached\\n'\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GIT_EXEC_PATH", reportedPath)
			t.Setenv("PATH", t.TempDir())
			t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
			t.Setenv("GIT_CONFIG_COUNT", "0")
			managed := Runtime{GitPath: gitPath, GitBin: t.TempDir()}

			spec, err := managed.Process(gitPath, "remote-https")
			if err != nil {
				t.Fatal(err)
			}
			out, err := spec.Output(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != "exact-helper-reached\n" {
				t.Fatalf("Git helper output = %q, want exact selected helper", out)
			}
			available, err := managed.GitTransportAvailable(context.Background(), "https")
			if err != nil {
				t.Fatal(err)
			}
			if !available {
				t.Fatalf("GitTransportAvailable lost executable helper at exact path %q", reportedPath)
			}
		})
	}
}
