// Package notify implements out-of-band delivery for hand notify and the
// watcher's in-process notify hook: reading config/notify's shell command
// template and running it with the message to send.
package notify

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNotConfigured means home has no config/notify template, so Send has
// nothing to run and delivers nothing. Callers must not treat this as
// success: the one property this package exists to protect is that "not
// configured" and "delivered" are never the same observable outcome.
var ErrNotConfigured = errors.New("no config/notify")

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

	run := exec.Command("sh", "-c", strings.TrimSpace(string(template)))
	run.Env = append(os.Environ(), "HAND_MESSAGE="+message)
	if out, err := run.CombinedOutput(); err != nil {
		return fmt.Errorf("notify command failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
