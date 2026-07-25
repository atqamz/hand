package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeFakeGHReleaseView(t *testing.T, tag string) {
	t.Helper()
	bin := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s' %q\n", tag)
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestUpdateCheckReportsAvailableUpdate(t *testing.T) {
	writeFakeGHReleaseView(t, "v0.5.0")

	cmd := newUpdateCmd("v0.1.0")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--check"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := "update available: v0.1.0 -> v0.5.0\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestUpdateCheckReportsUpToDate(t *testing.T) {
	writeFakeGHReleaseView(t, "v0.1.0")

	cmd := newUpdateCmd("v0.1.0")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--check"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := "hand v0.1.0 is up to date\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestUpdateWithoutCheckSkipsInstallWhenUpToDate(t *testing.T) {
	writeFakeGHReleaseView(t, "v0.1.0")

	cmd := newUpdateCmd("v0.1.0")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := "hand v0.1.0 is up to date\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestUpdateCheckReportsAvailableUpdateForDevBuild(t *testing.T) {
	writeFakeGHReleaseView(t, "v0.5.0")

	cmd := newUpdateCmd("dev")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--check"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := "update available: dev -> v0.5.0\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestUpdatePropagatesLatestTagError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	cmd := newUpdateCmd("v0.1.0")
	cmd.SetArgs([]string{"--check"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("want error when gh is unreachable")
	}
}
