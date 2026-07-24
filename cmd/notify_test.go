package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotifyWithoutConfigPrintsToStdoutOnly(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)

	var out bytes.Buffer
	cmd := newNotifyCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"hello world"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "notified: hello world\n" {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestNotifySubstitutesMessageAndExecutesTemplate(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "marker.txt")
	template := "printf '%s' \"{{message}}\" > " + marker
	if err := os.WriteFile(filepath.Join(home, "config", "notify"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := newNotifyCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"hello world"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if out.String() != "notified: hello world\n" {
		t.Fatalf("stdout = %q", out.String())
	}

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello world" {
		t.Fatalf("marker content = %q, want %q", got, "hello world")
	}
}

func TestNotifyFailureWarnsButDoesNotBlock(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "notify"), []byte("exit 1"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	cmd := newNotifyCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"hello world"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "warning") {
		t.Fatalf("stderr = %q, want warning", errOut.String())
	}
	if out.String() != "notified: hello world\n" {
		t.Fatalf("stdout = %q", out.String())
	}
}
