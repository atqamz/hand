package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/flock"
	"github.com/atqamz/hand/internal/gitcfg"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

const launchGrace = 2 * time.Minute

func fleetEnv(environ []string) []string {
	out := make([]string, 0, len(environ)+1)
	found := false
	for _, kv := range environ {
		if name, _, _ := strings.Cut(kv, "="); name == "PI_NOTIFICATIONS" || runtime.GOOS == "windows" && strings.EqualFold(name, "PI_NOTIFICATIONS") {
			if !found {
				out = append(out, "PI_NOTIFICATIONS=0")
				found = true
			}
			continue
		}
		out = append(out, kv)
	}
	if !found {
		out = append(out, "PI_NOTIFICATIONS=0")
	}
	return out
}

func (r *runner) luvus(ctx context.Context) (luvus.Client, luvus.Capabilities, error) {
	if r.fleet.ID == "" {
		return luvus.Client{}, luvus.Capabilities{}, errors.New("hand reached luvus before claiming its fleet")
	}
	session := fleet.Session(r.fleet.ID)
	c := r.client(session)
	caps, err := luvus.Ensure(ctx, c, func() error {
		bin, err := r.luvusBin()
		if err != nil {
			return err
		}
		dir := filepath.Join(r.home, "luvus")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		return luvus.StartServer(ctx, bin, session, dir, fleetEnv(r.env.Environ()))
	})
	return c, caps, runtimeErr(err)
}

func (r *runner) client(session string) luvus.Client {
	if runtime.GOOS != "windows" || r.env.Getenv("HAND_LUVUS_SOCKET") != "" {
		return luvus.Client{Socket: luvus.SocketPath(r.env.Getenv, session)}
	}
	known, _ := r.addrs.Load(session)
	socket, _ := known.(string)
	return luvus.Client{Socket: socket, Relist: func(ctx context.Context, stale string) (string, error) {
		if now, ok := r.addrs.Load(session); ok && now != stale {
			return now.(string), nil
		}
		bin, err := r.luvusBin()
		if err != nil {
			return "", err
		}
		addr, _, err := luvus.Address(ctx, bin, session, r.env.Environ())
		if err == nil {
			r.addrs.Store(session, addr)
		}
		return addr, err
	}}
}

func (r *runner) managedSupervisor(ctx context.Context, st *state.Store) (bool, error) {
	sup, ok, err := st.LiveSupervisor(ctx)
	if err != nil || !ok {
		return false, err
	}
	caps, err := r.client(fleet.Session(r.fleet.ID)).Check(ctx)
	return err == nil && sup.Managed(caps.ServerGeneration), nil
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

func stateTerminal(t luvus.Terminal, socket string) state.Terminal {
	return state.Terminal{ServerGeneration: t.ServerGeneration, TerminalID: t.TerminalID, PaneID: t.PaneID, PID: t.Root.PID, StartMarker: t.Root.StartMarker, Socket: socket}
}

func (r *runner) ownSocket() string {
	session := fleet.Session(r.fleet.ID)
	if runtime.GOOS == "windows" && r.env.Getenv("HAND_LUVUS_SOCKET") == "" {
		return session
	}
	return luvus.SocketPath(r.env.Getenv, session)
}

func foreignNote(socket string) string { return "terminal belongs to another luvus (" + socket + ")" }

func (r *runner) foreignSocket(t state.Terminal) string {
	if t.Socket != "" && !sameSocket(t.Socket, r.ownSocket()) {
		return t.Socket
	}
	return ""
}

func sameSocket(a, b string) bool {
	if resolveSocket(a) == resolveSocket(b) {
		return true
	}
	x, err := os.Stat(a)
	if err != nil {
		return false
	}
	y, err := os.Stat(b)
	return err == nil && os.SameFile(x, y)
}

func resolveSocket(p string) string {
	p = filepath.Clean(p)
	if dir, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(dir, filepath.Base(p))
	}
	return p
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
	if err != nil {
		return err
	}
	if to == "" {
		if a.Status == state.AttemptRunning && a.Socket == "" {
			return st.StampAttemptSocket(ctx, a.ID, r.ownSocket())
		}
		return nil
	}
	if sock := r.foreignSocket(a.Terminal); sock != "" {
		note := r.requester().noteSettled(foreignNote(sock), false)
		if _, err := st.EndAttemptWithScreen(ctx, a.ID, state.AttemptInterrupted, note, r.screens.take(a.ID)); err != nil && !errors.Is(err, state.ErrConflict) {
			return err
		}
		return nil
	}
	signalled := rootAlive(a.PID, a.StartMarker)
	if err := stopRoot(a.PID, a.StartMarker); err != nil {
		return err
	}
	who, err := closeLabelled(ctx, c, "hand-"+state.AttemptRef(a.ID), a.TerminalID, r.requester())
	sweepWorktree(a.Worktree)
	if err != nil {
		if a.Status == state.AttemptLaunching {
			return nil
		}
		reason += "; terminal cleanup failed: " + err.Error()
	}
	if _, err := st.EndAttemptWithScreen(ctx, a.ID, to, who.noteSettled(reason, signalled), r.screens.take(a.ID)); err != nil && !errors.Is(err, state.ErrConflict) {
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
	if ok, err := flock.Lock(f, wait); !ok {
		_ = f.Close()
		return nil, false, err
	}
	return func() { _ = flock.Release(f) }, true, nil
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
			return state.AttemptExited, r.exitReason(ctx, t.TerminalID), nil
		}
		return "", "", runtimeErr(err)
	}
	if health == "gone" {
		return state.AttemptExited, r.exitReason(ctx, t.TerminalID), nil
	}
	return "", "", nil
}

func (r *runner) exitReason(ctx context.Context, terminalID string) string {
	if r.exits == nil {
		return "terminal exited"
	}
	return r.exits.wait(ctx, terminalID)
}

func closeLabelled(ctx context.Context, c luvus.Client, label, terminalID string, who requester) (requester, error) {
	return closeTerminals(ctx, c, who, func(t luvus.Terminal) bool {
		return t.Label == label || (terminalID != "" && t.TerminalID == terminalID)
	})
}

func closeErr(_ requester, err error) error { return err }

func closeTerminals(ctx context.Context, c luvus.Client, who requester, match func(luvus.Terminal) bool) (requester, error) {
	terms, err := c.Inventory(ctx)
	if err != nil {
		return who, runtimeErr(err)
	}
	for _, t := range terms {
		if !match(t) {
			continue
		}
		_, closed, err := stopWorker(ctx, c, t, who)
		if err != nil {
			return who, err
		}
		who = closed
	}
	return who, nil
}

func repoGit(ctx context.Context, st *state.Store, p state.Project) (gitcfg.Repo, error) {
	recorded := p.Settings
	if recorded == "" {
		now, err := gitcfg.Read(ctx, p.Repo)
		if err != nil {
			return gitcfg.Repo{}, err
		}
		if recorded, err = st.AdoptProject(ctx, p.Name, now.Digest()); err != nil {
			return gitcfg.Repo{}, err
		}
	}
	g, err := gitcfg.Open(ctx, p.Name, p.Repo, recorded)
	if err != nil {
		return g, err
	}
	return g, g.Check(ctx)
}
