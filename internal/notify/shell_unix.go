//go:build !windows

package notify

import (
	"context"
	"os/exec"
)

func commandContext(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", command)
}
