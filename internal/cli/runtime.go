package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

const launchGrace = 2 * time.Minute

func (r *runner) luvus(ctx context.Context) (luvus.Client, luvus.Capabilities, error) {
	c := luvus.Client{Socket: luvus.SocketPath(r.env.Getenv, luvus.Session)}
	caps, err := luvus.Ensure(ctx, c, func() error {
		bin, err := harness.LookPath("luvus", r.env.Getenv("PATH"))
		if err != nil {
			return err
		}
		dir := filepath.Join(r.home, "luvus")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		return luvus.StartServer(bin, luvus.Session, dir, r.env.Environ())
	})
	return c, caps, runtimeErr(err)
}

func runtimeErr(err error) error {
	switch luvus.Code(err) {
	case "":
		return err
	case "invalid_request", "invalid_params":
		return fmt.Errorf("%w: %w", state.ErrInvalid, err)
	case "not_found":
		return fmt.Errorf("%w: %w", state.ErrNotFound, err)
	}
	return fmt.Errorf("%w: %w", state.ErrConflict, err)
}

func terminal(a state.Attempt) luvus.Terminal {
	return luvus.Terminal{ServerGeneration: a.ServerGeneration, TerminalID: a.TerminalID, PaneID: a.PaneID, Root: luvus.Root{PID: a.PID, StartMarker: a.StartMarker}}
}

func (r *runner) withAttempts(fn func(context.Context, *state.Store, luvus.Client) error) error {
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	c, caps, err := r.luvus(ctx)
	if err != nil {
		return err
	}
	if err := r.sync(ctx, st, c, caps); err != nil {
		return err
	}
	return fn(ctx, st, c)
}

func (r *runner) sync(ctx context.Context, st *state.Store, c luvus.Client, caps luvus.Capabilities) error {
	live, err := st.LiveAttempts(ctx)
	if err != nil {
		return err
	}
	for _, a := range live {
		to, reason, err := r.observe(ctx, c, caps, a)
		if err != nil {
			return err
		}
		if to == "" {
			continue
		}
		if _, err := st.EndAttempt(ctx, a.ID, to, reason); err != nil && !errors.Is(err, state.ErrConflict) {
			return err
		}
	}
	return nil
}

func (r *runner) observe(ctx context.Context, c luvus.Client, caps luvus.Capabilities, a state.Attempt) (string, string, error) {
	if a.Status == state.AttemptLaunching {
		created, err := time.Parse(time.RFC3339Nano, a.CreatedAt)
		if err != nil {
			return "", "", err
		}
		if r.env.Now().Sub(created) > launchGrace {
			return state.AttemptFailed, "launch did not finish", nil
		}
		return "", "", nil
	}
	if a.ServerGeneration != caps.ServerGeneration {
		return state.AttemptInterrupted, "luvus server restarted", nil
	}
	health, err := c.Validate(ctx, terminal(a))
	if err != nil {
		switch luvus.Code(err) {
		case "stale_server":
			return state.AttemptInterrupted, "luvus server restarted", nil
		case "process_mismatch":
			return state.AttemptInterrupted, "terminal root process changed", nil
		case "stale_terminal", "stale_route", "terminal_gone":
			return state.AttemptExited, "terminal exited", nil
		}
		return "", "", runtimeErr(err)
	}
	if health == "gone" {
		return state.AttemptExited, "terminal exited", nil
	}
	return "", "", nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return string(out), nil
}
