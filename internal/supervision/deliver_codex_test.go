package supervision

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestProbeCodexQueueAcceptsOnlyTheRealPrimitive(t *testing.T) {
	help := "usage: codex queue --thread <THREAD> --message <TEXT>"
	if err := ProbeCodexQueue(context.Background(), func(context.Context, string, []string, []string) (string, error) {
		return help, nil
	}); err != nil {
		t.Fatalf("probe = %v, want a queue that names both flags accepted", err)
	}
	for _, broken := range []string{
		"usage: codex queue", // older build: no flags named
		"usage: codex queue --message <TEXT>",
		"usage: codex queue --thread <THREAD>",
	} {
		if err := ProbeCodexQueue(context.Background(), func(context.Context, string, []string, []string) (string, error) {
			return broken, nil
		}); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("probe(%q) = %v, want ErrUnsupported", broken, err)
		}
	}
	if err := ProbeCodexQueue(context.Background(), func(context.Context, string, []string, []string) (string, error) {
		return "", exec.ErrNotFound
	}); !errors.Is(err, ErrCodexExecutableUnavailable) || err.Error() != "unsupported host integration: codex executable is unavailable on PATH" {
		t.Fatalf("probe of a missing binary = %v, want the exact hermetic-unavailable reason", err)
	}
}

func TestDeliverCodexQueueUsesStructuredArgv(t *testing.T) {
	var gotExe string
	var gotArgv []string
	err := DeliverCodexQueue(context.Background(), func(_ context.Context, exe string, argv []string, _ []string) (string, error) {
		gotExe, gotArgv = exe, argv
		return "", nil
	}, " thread-9 ", "wake text")
	if err != nil {
		t.Fatal(err)
	}
	if gotExe != "codex" || !reflect.DeepEqual(gotArgv, []string{"queue", "--thread", "thread-9", "--message", "wake text"}) {
		t.Fatalf("delivery = %s %v, want one argv array, never a shell string", gotExe, gotArgv)
	}

	if err := DeliverCodexQueue(context.Background(), func(context.Context, string, []string, []string) (string, error) {
		t.Fatal("must not spawn without a thread")
		return "", nil
	}, "  ", "wake"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("empty thread = %v, want ErrUnsupported", err)
	}

	err = DeliverCodexQueue(context.Background(), func(context.Context, string, []string, []string) (string, error) {
		return "error: no such thread", errors.New("exit status 1")
	}, "thread-9", "wake")
	if err == nil || !strings.Contains(err.Error(), "no such thread") {
		t.Fatalf("rejected queue = %v, want the bounded host line carried", err)
	}
}
