package supervision

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// CodexThreadEnv is where a live Codex runtime exposes the exact current
// thread. It is routing evidence only, never workflow currentness.
const CodexThreadEnv = "CODEX_THREAD_ID"

// ErrUnsupported names a host whose installed build lacks the primitive its
// unattended turn delivery needs. Callers must report it as unsupported or
// degraded, never as healthy.
var ErrUnsupported = errors.New("unsupported host integration")

// CommandRunner executes one structured argv with env additions and returns
// combined output. exec.CommandContext with an argv slice is the only allowed
// shape: no shell concatenation anywhere in a delivery path.
type CommandRunner func(ctx context.Context, exe string, argv []string, env []string) (string, error)

// RunCommand is the production CommandRunner.
func RunCommand(ctx context.Context, exe string, argv []string, env []string) (string, error) {
	cmd := exec.CommandContext(ctx, exe, argv...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ProbeCodexQueue capability-probes behavior, not product name: only an
// installed `codex queue` that accepts --thread/--message qualifies for
// unattended same-thread turn delivery.
func ProbeCodexQueue(ctx context.Context, run CommandRunner) error {
	out, err := run(ctx, "codex", []string{"queue", "--help"}, nil)
	if err != nil {
		return fmt.Errorf("%w: codex queue is unavailable: %s", ErrUnsupported, firstLine(out))
	}
	for _, flag := range []string{"--thread", "--message"} {
		if !strings.Contains(out, flag) {
			return fmt.Errorf("%w: codex queue does not accept %s", ErrUnsupported, flag)
		}
	}
	return nil
}

// DeliverCodexQueue enqueues one bounded wake on the exact live thread.
// Acceptance means enqueued only; the resulting turn running `hand orient` is
// the stronger progress witness, recorded by orient itself.
func DeliverCodexQueue(ctx context.Context, run CommandRunner, threadID, message string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return fmt.Errorf("%w: %s is not set, so no live Codex thread can be addressed", ErrUnsupported, CodexThreadEnv)
	}
	out, err := run(ctx, "codex", []string{"queue", "--thread", threadID, "--message", message}, nil)
	if err != nil {
		return fmt.Errorf("codex queue rejected the wake on thread %s: %s: %w", threadID, firstLine(out), err)
	}
	return nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return s
}
