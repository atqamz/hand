package notify

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/shellquote"
)

func TestSendReturnsErrNotConfiguredWithNoTemplate(t *testing.T) {
	home := t.TempDir()

	err := Send(home, "hello")
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Send() error = %v, want ErrNotConfigured", err)
	}
}

func TestSendReturnsErrNotConfiguredWithAnEmptyTemplate(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "notify"), []byte("  \n\t\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Send(home, "hello")
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Send() error = %v, want ErrNotConfigured", err)
	}
}

func TestSendReportsActionableErrorWhenShIsMissing(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "notify"), []byte("printf '%s' \"$HAND_MESSAGE\""), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())

	err := Send(home, "hello")
	if err == nil {
		t.Fatal("Send() error = nil, want missing-shell failure")
	}
	if errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Send() error = %v, want a configured-template failure", err)
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("Send() error = %v, want exec.ErrNotFound", err)
	}
	for _, want := range []string{"config/notify", "posix", "sh", "path"} {
		if !strings.Contains(strings.ToLower(err.Error()), want) {
			t.Fatalf("Send() error = %v, want it to mention %q", err, want)
		}
	}
}

func TestSendDoesNotRequireShWhenNotificationIsUnconfigured(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	t.Run("missing", func(t *testing.T) {
		err := Send(t.TempDir(), "hello")
		if !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("Send() error = %v, want ErrNotConfigured", err)
		}
	})

	t.Run("empty", func(t *testing.T) {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "config", "notify"), []byte("  \n\t\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		err := Send(home, "hello")
		if !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("Send() error = %v, want ErrNotConfigured", err)
		}
	})
}

func TestSendRunsTheTemplateWithTheMessage(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "marker.txt")
	template := "printf '%s' \"$HAND_MESSAGE\" > " + shellquote.Quote(marker)
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

func TestSendWithWakeProvidesStructuredHintAlongsideMessage(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "marker.txt")
	template := "printf '%s\\n%s' \"$HAND_MESSAGE\" \"$HAND_WAKE\" > " + shellquote.Quote(marker)
	if err := os.WriteFile(filepath.Join(home, "config", "notify"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SendWithWake(home, "blocked task-1", map[string]string{"fleet_id": "f_one", "currentness": "c_one"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "blocked task-1\n{\"currentness\":\"c_one\",\"fleet_id\":\"f_one\"}" {
		t.Fatalf("marker content = %q, want message and structured wake", got)
	}
}

func TestSendReportsTemplateFailureRatherThanSwallowingIt(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := "echo boom >&2; exit 1"
	if err := os.WriteFile(filepath.Join(home, "config", "notify"), []byte(template), 0o644); err != nil {
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

func TestSendCountsADeliveryWhoseTemplateBackgroundsAChildAsDelivered(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "marker.txt")
	template := "printf '%s' \"$HAND_MESSAGE\" > " + shellquote.Quote(marker) + "; sleep 3 &"
	if err := os.WriteFile(filepath.Join(home, "config", "notify"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if err := Send(home, "hello world"); err != nil {
		t.Fatalf("Send() error = %v, want nil: the template's own process exited 0, only an orphaned pipe holder lingered", err)
	}
	if elapsed := time.Since(start); elapsed >= sendTimeout {
		t.Fatalf("Send() took %s, want it to stop waiting on the orphaned pipe well before sendTimeout", elapsed)
	}

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello world" {
		t.Fatalf("marker content = %q, want %q", got, "hello world")
	}
}

func TestSendGivesUpOnAHangingTemplateRatherThanBlocking(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "notify"), []byte("sleep 60"), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := sendTimeout
	sendTimeout = 100 * time.Millisecond
	defer func() { sendTimeout = restore }()

	start := time.Now()
	err := Send(home, "hello world")
	if err == nil {
		t.Fatal("Send() error = nil, want the timeout")
	}
	if errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Send() error = %v, want a timeout distinct from ErrNotConfigured", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Send() error = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("Send() took %s, want it bounded by sendTimeout", elapsed)
	}
}
