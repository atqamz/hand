package update

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/proc"
)

func Systemctl(ctx context.Context, env []string, args ...string) error {
	path := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	bin, err := harness.LookPath("systemctl", path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append([]string{"--user"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Env, cmd.Stdout, cmd.Stderr, cmd.WaitDelay = env, &stdout, &stderr, time.Second
	proc.NewGroup(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl --user %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()+stdout.String()))
	}
	return nil
}
