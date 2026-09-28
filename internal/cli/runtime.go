package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

const launchGrace = 2 * time.Minute

func (r *runner) luvus(ctx context.Context) (luvus.Client, luvus.Capabilities, error) {
	if r.fleet.ID == "" {
		return luvus.Client{}, luvus.Capabilities{}, errors.New("hand reached luvus before claiming its fleet")
	}
	session := fleet.Session(r.fleet.ID)
	c := luvus.Client{Socket: luvus.SocketPath(r.env.Getenv, session)}
	caps, err := luvus.Ensure(ctx, c, func() error {
		bin, err := harness.LookPath("luvus", r.env.Getenv("PATH"))
		if err != nil {
			return err
		}
		dir := filepath.Join(r.home, "luvus")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		return luvus.StartServer(ctx, bin, session, fleet.LuvusUnit(r.fleet.ID), dir, r.env.Environ())
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

func terminal(t state.Terminal) luvus.Terminal {
	return luvus.Terminal{ServerGeneration: t.ServerGeneration, TerminalID: t.TerminalID, PaneID: t.PaneID, Root: luvus.Root{PID: t.PID, StartMarker: t.StartMarker}}
}

func (r *runner) withAttempts(fn func(context.Context, *state.Store, luvus.Client) error) error {
	return r.withSupervisor(func(ctx context.Context, st *state.Store, c luvus.Client, _ luvus.Capabilities) error {
		return fn(ctx, st, c)
	})
}

func (r *runner) withSupervisor(fn func(context.Context, *state.Store, luvus.Client, luvus.Capabilities) error) error {
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
	return fn(ctx, st, c, caps)
}

func (r *runner) sync(ctx context.Context, st *state.Store, c luvus.Client, caps luvus.Capabilities) error {
	live, err := st.LiveAttempts(ctx)
	if err != nil {
		return err
	}
	for _, a := range live {
		unlock, ok, err := r.attemptLock(a.ID, false)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		err = r.settle(ctx, st, c, caps, a.ID)
		unlock()
		if err != nil {
			return err
		}
	}
	return r.settleSupervisor(ctx, st, c, caps)
}

func (r *runner) settle(ctx context.Context, st *state.Store, c luvus.Client, caps luvus.Capabilities, id int64) error {
	a, err := st.Attempt(ctx, id)
	if err != nil || !a.Live() {
		return err
	}
	to, reason, err := r.observeTerminal(ctx, c, caps, a.Status, a.CreatedAt, a.Terminal)
	if err != nil || to == "" {
		return err
	}
	if err := stopRoot(a.PID, a.StartMarker); err != nil {
		return err
	}
	if err := closeLabelled(ctx, c, "hand-"+state.AttemptRef(a.ID), a.TerminalID); err != nil {
		if a.Status == state.AttemptLaunching {
			return nil
		}
		reason += "; terminal cleanup failed: " + err.Error()
	}
	if _, err := st.EndAttempt(ctx, a.ID, to, reason); err != nil && !errors.Is(err, state.ErrConflict) {
		return err
	}
	return nil
}

func (r *runner) attemptLock(id int64, wait bool) (func(), bool, error) {
	return r.lock(state.AttemptRef(id), wait)
}

func (r *runner) lock(name string, wait bool) (func(), bool, error) {
	dir := filepath.Join(r.home, "locks")
	if err := os.Mkdir(dir, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, false, err
	}
	f, err := os.OpenFile(filepath.Join(dir, name+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	how := syscall.LOCK_EX
	if !wait {
		how |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() { _ = f.Close() }, true, nil
}

func (r *runner) observeTerminal(ctx context.Context, c luvus.Client, caps luvus.Capabilities, status, createdAt string, t state.Terminal) (string, string, error) {
	if status == state.AttemptLaunching {
		created, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return "", "", err
		}
		if r.env.Now().Sub(created) > launchGrace {
			return state.AttemptFailed, "launch did not finish", nil
		}
		return "", "", nil
	}
	if t.ServerGeneration != caps.ServerGeneration {
		return state.AttemptInterrupted, "luvus server restarted", nil
	}
	health, err := c.Validate(ctx, terminal(t))
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

func closeLabelled(ctx context.Context, c luvus.Client, label, terminalID string) error {
	return closeTerminals(ctx, c, func(t luvus.Terminal) bool {
		return t.Label == label || (terminalID != "" && t.TerminalID == terminalID)
	})
}

func closeTerminals(ctx context.Context, c luvus.Client, match func(luvus.Terminal) bool) error {
	terms, err := c.Inventory(ctx)
	if err != nil {
		return runtimeErr(err)
	}
	for _, t := range terms {
		if !match(t) {
			continue
		}
		if err := stopWorker(ctx, c, t); err != nil {
			return err
		}
	}
	return nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return string(out), nil
}
