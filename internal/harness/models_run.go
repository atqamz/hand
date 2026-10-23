package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/proc"
)

const maxStderr = 8 << 10

var maxOutput = 4 << 20

var baseEnv = []string{"PATH", "HOME", "USERPROFILE", "TMPDIR", "TEMP", "TMP", "SYSTEMROOT", "WINDIR", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"}

type runner struct {
	bin     string
	environ []string
	dir     string
}

func childEnv(environ, prefixes []string) []string {
	var out []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		name = strings.ToUpper(name)
		if slices.Contains(baseEnv, name) || slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(name, p) }) {
			out = append(out, kv)
		}
	}
	return out
}

type capped struct {
	buf    bytes.Buffer
	max    int
	over   bool
	cancel context.CancelFunc
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); len(p) > room {
		c.buf.Write(p[:room])
		c.over = true
		if c.cancel != nil {
			c.cancel()
		}
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (r runner) run(parent context.Context, timeout time.Duration, stdin string, args ...string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.bin, args...)
	out, errOut := &capped{max: maxOutput, cancel: cancel}, &capped{max: maxStderr}
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr, cmd.WaitDelay = r.environ, r.dir, out, errOut, time.Second
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	proc.NoWindow(cmd)
	proc.KillGroupOnCancel(cmd)
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case parent.Err() != nil:
		return nil, nil, parent.Err()
	case out.over:
		return out.buf.Bytes(), nil, fmt.Errorf("printed more than %d bytes", maxOutput)
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return out.buf.Bytes(), nil, fmt.Errorf("timed out after %v", timeout)
	case errors.Is(err, exec.ErrWaitDelay):
		err = nil
	case errors.As(err, &exit):
		err = fmt.Errorf("exited with code %d", exit.ExitCode())
	case err != nil:
		err = errors.New("could not run")
	}
	return out.buf.Bytes(), errOut.buf.Bytes(), err
}

func (r runner) version(ctx context.Context) string {
	out, _, err := r.run(ctx, 5*time.Second, "", "--version")
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return clip(line, 64)
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return ' '
		}
		return r
	}, s)), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
