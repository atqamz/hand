// Package notify implements out-of-band delivery for hand notify and the
// watcher's in-process notify hook: reading config/notify's shell command
// template and running it with the message to send.
package notify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrNotConfigured means home has no config/notify template, or an empty one, so
// Send runs nothing. Callers must not treat it as success: this package exists to
// keep "not configured" and "delivered" from being the same observable outcome.
var ErrNotConfigured = errors.New("no config/notify")

// Bounds the template's own run: the watcher calls Send inline in its poll loop,
// so an unbounded template (the documented curl example has no --max-time) would
// wedge polling, --until-event's timeout and shutdown alike. A var so tests cut it.
var sendTimeout = 10 * time.Second

// Send runs config/notify's shell command template with message available as
// $HAND_MESSAGE, in-process rather than through the hand notify subcommand,
// and reports whether the template actually ran and succeeded.
func Send(home, message string) error {
	template, err := os.ReadFile(filepath.Join(home, "config", "notify"))
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotConfigured
		}
		return fmt.Errorf("read config/notify: %w", err)
	}

	command := strings.TrimSpace(string(template))
	if command == "" {
		return ErrNotConfigured
	}

	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	run := exec.CommandContext(ctx, "sh", "-c", command)
	run.Env = append(os.Environ(), "HAND_MESSAGE="+message)
	// Without WaitDelay, a template that backgrounds a child holding the output
	// pipe keeps CombinedOutput waiting past the kill, defeating the timeout.
	run.WaitDelay = time.Second
	out, err := run.CombinedOutput()
	if err != nil {
		// WaitDelay only bounds waiting on a pipe an orphaned grandchild still
		// holds ("... &" templates), where the template's own process exited 0 and
		// the send happened. A real failure is a non-zero exit, reported ahead of it.
		if errors.Is(err, exec.ErrWaitDelay) && run.ProcessState != nil && run.ProcessState.Success() {
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("notify command timed out after %s: %w: %s", sendTimeout, ctxErr, strings.TrimSpace(string(out)))
		}
		return fmt.Errorf("notify command failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
