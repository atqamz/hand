package notify

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSendReturnsErrNotConfiguredWithNoTemplate(t *testing.T) {
	home := t.TempDir()

	err := Send(home, "hello")
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Send() error = %v, want ErrNotConfigured", err)
	}
}

func TestSendRunsTheTemplateWithTheMessage(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "marker.txt")
	template := "printf '%s' \"$HAND_MESSAGE\" > " + marker
	if err := os.WriteFile(filepath.Join(home, "config", "notify"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Send(home, "hello world"); err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello world" {
		t.Fatalf("marker content = %q, want %q", got, "hello world")
	}
}

func TestSendReportsTemplateFailureRatherThanSwallowingIt(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "notify"), []byte("echo boom >&2; exit 1"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Send(home, "hello world")
	if err == nil {
		t.Fatal("Send() error = nil, want the template's failure")
	}
	if errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Send() error = %v, want a failure distinct from ErrNotConfigured", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Send() error = %v, want it to carry the command's output", err)
	}
}
