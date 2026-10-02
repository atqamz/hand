package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

type Options struct {
	Target                              string
	From                                Build
	Channel                             string
	Check                               bool
	Root, HandBase, LuvusBase, OS, Arch string
	Env                                 []string
	Getenv                              func(string) string
	Now                                 func() time.Time
	Server                              func(ctx context.Context, unit string) (luvus.Server, bool)
	Hold                                func()
	Cgroup                              string
}

type UnitResult struct{ Name, Action, Result string }

type FleetResult struct{ Name, Init, Luvus string }

type Report struct {
	Status         string
	From, To       Build
	PinFrom, PinTo string
	Backups        []string
	Units          []UnitResult
	Fleets         []FleetResult
	Help           []string
	Failed         bool
}

func Target(exe string) (string, error) {
	return filepath.EvalSymlinks(exe)
}

func Run(ctx context.Context, o Options) (Report, error) {
	rep := Report{From: o.From}
	if o.Channel != "edge" && o.Channel != "stable" {
		return rep, fmt.Errorf("%w: update: --channel must be edge or stable", state.ErrInvalid)
	}
	tmp, err := os.MkdirTemp(filepath.Dir(o.Target), ".hand-update-")
	if err != nil {
		return rep, fmt.Errorf("update: cannot replace %s: %w", o.Target, err)
	}
	defer os.RemoveAll(tmp)
	if rep.To, err = FetchHand(ctx, o.HandBase, o.Channel, o.OS, o.Arch, tmp, o.Env); err != nil {
		return rep, err
	}
	if rep.To.Schema < state.SchemaVersion {
		return rep, fmt.Errorf("update: %s reads state schema %d, older than this fleet's %d", rep.To.Version, rep.To.Schema, state.SchemaVersion)
	}
	if rep.To.Channel != o.Channel {
		return rep, fmt.Errorf("update: the download is a %s build, not %s", rep.To.Channel, o.Channel)
	}
	pin, pinned, err := luvus.LoadPin(o.Root)
	if err != nil {
		return rep, err
	}
	rep.PinFrom = "none"
	if pinned {
		rep.PinFrom = pin.Version
	}
	rep.PinTo = rep.PinFrom
	repin := Newer(rep.To.Luvus, pin.Version)
	if repin {
		rep.PinTo = rep.To.Luvus
	}
	fleets, err := fleet.List(o.Root)
	if err != nil {
		return rep, err
	}
	server := o.Server
	if server == nil {
		server = func(ctx context.Context, unit string) (luvus.Server, bool) {
			return luvus.RunningServer(ctx, o.Env, unit)
		}
	}
	matches := func(e fleet.Entry) bool {
		srv, known := server(ctx, fleet.LuvusUnit(e.ID))
		return !known || !pinned || srv.SHA256 == pin.SHA256
	}
	current := rep.To.Channel == o.From.Channel && rep.To.Commit == o.From.Commit
	if current && !repin && !slices.ContainsFunc(fleets, func(e fleet.Entry) bool { return e.State == "ok" && !matches(e) }) {
		rep.Status = "up to date"
		return rep, nil
	}
	if o.Check {
		for _, e := range fleets {
			fr := FleetResult{Name: e.Name, Init: "would run", Luvus: "kept"}
			if e.State != "ok" {
				fr.Name, fr.Init = e.ID, "skipped: "+e.State
			} else if repin || !matches(e) {
				fr.Luvus = "would stay pending"
				if _, ok, err := o.switchable(ctx, e); err == nil && ok {
					fr.Luvus = "would switch"
				}
			}
			rep.Fleets = append(rep.Fleets, fr)
		}
		rep.Status = "checked"
		return rep, nil
	}
	held := false
	hold := func() {
		if o.Hold != nil && !held {
			o.Hold()
		}
		held = true
	}
	if repin {
		bin, err := FetchLuvus(ctx, o.LuvusBase, rep.To.Luvus, o.OS, o.Arch, tmp)
		if err != nil {
			return rep, err
		}
		hold()
		if pin, err = luvus.Keep(ctx, o.Root, bin, o.Env, o.Now()); err != nil {
			return rep, err
		}
		pinned, rep.PinTo = true, pin.Version
	}
	hold()
	if !current {
		if rep.Backups, err = Backup(ctx, o.Root, o.Target, fleets, o.Now()); err != nil {
			return rep, err
		}
		if err := swap(rep.To.Path, o.Target); err != nil {
			return rep, err
		}
		if err := Prune(o.Root, fleets); err != nil {
			rep.Help = append(rep.Help, fmt.Sprintf("could not remove old backups (%v)", err))
		}
	}
	units, err := Units(ctx, o.Env, o.Target)
	listed := err == nil
	switch {
	case errors.Is(err, ErrNoSystemctl):
		rep.Help = append(rep.Help, fmt.Sprintf("systemctl was not found; restart the board and watch units that run %s by hand", o.Target))
	case err != nil:
		rep.fail(fmt.Sprintf("could not list Hand's units (%v); restart the board and watch units with `systemctl --user restart`", err))
	}
	for _, e := range fleets {
		fr := FleetResult{Name: e.Name, Init: "ok", Luvus: "kept"}
		if e.State != "ok" {
			fr.Name, fr.Init = e.ID, "skipped: "+e.State
			rep.Fleets = append(rep.Fleets, fr)
			continue
		}
		var watches []Unit
		for _, u := range units {
			if u.Command == "watch" && u.Active && filepath.Clean(u.Home) == filepath.Clean(e.Home) {
				watches = append(watches, u)
			}
		}
		stopped := listed
		for _, u := range watches {
			stopped = rep.unit(ctx, o, u.Name, "stop") && stopped
		}
		if !matches(e) {
			if stopped {
				fr.Luvus = rep.switchLuvus(ctx, o, e)
			} else {
				fr.Luvus = "pending"
				rep.Help = append(rep.Help, pendingLine(e))
			}
		}
		for _, u := range watches {
			rep.unit(ctx, o, u.Name, "start")
		}
		rep.Fleets = append(rep.Fleets, fr)
	}
	for _, u := range units {
		if u.Command == "board" && u.Active {
			rep.unit(ctx, o, u.Name, "restart")
		}
	}
	for i, e := range fleets {
		if e.State != "ok" {
			continue
		}
		if err := child(ctx, o, e.Home, "init"); err != nil {
			rep.Fleets[i].Init = "failed: " + err.Error()
			rep.fail(fmt.Sprintf("`hand init` failed in %s (%v); run it there", e.Home, err))
		}
	}
	rep.Status = "updated"
	return rep, nil
}

func (r *Report) fail(help string) {
	r.Failed = true
	r.Help = append(r.Help, help)
}

func (r *Report) unit(ctx context.Context, o Options, name, action string) bool {
	res := UnitResult{Name: name, Action: action, Result: "ok"}
	err := Systemctl(ctx, o.Env, action, name)
	if err != nil {
		res.Result = "failed: " + err.Error()
		r.fail(fmt.Sprintf("`systemctl --user %s %s` failed (%v); run it again", action, name, err))
	}
	r.Units = append(r.Units, res)
	return err == nil
}

func pendingLine(e fleet.Entry) string {
	return "At a quiet time: `systemctl --user stop " + fleet.LuvusUnit(e.ID) + ".service`, then `hand supervisor resume` in " + e.Home
}

func (o Options) inside(e fleet.Entry) bool {
	return strings.Contains(o.Cgroup, "/"+fleet.LuvusUnit(e.ID)+".service")
}

func (o Options) switchable(ctx context.Context, e fleet.Entry) (live, ok bool, err error) {
	if o.inside(e) {
		return false, false, nil
	}
	st, err := state.Open(filepath.Join(e.Home, "hand.db"), o.Now)
	if err != nil {
		return false, false, err
	}
	defer st.Close()
	if _, live, err = st.LiveSupervisor(ctx); err != nil {
		return false, false, err
	}
	ok, err = quiet(ctx, st, luvus.Client{Socket: luvus.SocketPath(o.Getenv, fleet.Session(e.ID))})
	return live, ok, err
}

func (r *Report) switchLuvus(ctx context.Context, o Options, e fleet.Entry) string {
	unit := fleet.LuvusUnit(e.ID) + ".service"
	pending := pendingLine(e)
	live, ok, err := o.switchable(ctx, e)
	switch {
	case err != nil:
		r.fail(fmt.Sprintf("could not read %s (%v). %s", e.Home, err, pending))
		return "failed: " + err.Error()
	case o.inside(e):
		r.Help = append(r.Help, "This hand update runs inside "+fleet.LuvusUnit(e.ID)+", so it leaves that server alone; run it from outside the fleet's Luvus panes. "+pending)
		return "pending"
	case !ok:
		r.Help = append(r.Help, pending)
		return "pending"
	}
	if live {
		if err := child(ctx, o, e.Home, "supervisor", "stop"); err != nil {
			r.fail(fmt.Sprintf("`hand supervisor stop` failed in %s (%v). %s", e.Home, err, pending))
			if err := child(ctx, o, e.Home, "supervisor", "resume"); err != nil {
				r.fail(fmt.Sprintf("`hand supervisor resume` failed in %s (%v); check the supervisor there", e.Home, err))
			}
			return "failed: " + err.Error()
		}
	}
	stopErr := Systemctl(ctx, o.Env, "stop", unit)
	res := UnitResult{Name: unit, Action: "stop", Result: "ok"}
	if stopErr != nil {
		res.Result = "failed: " + stopErr.Error()
		r.fail(fmt.Sprintf("`systemctl --user stop %s` failed (%v). %s", unit, stopErr, pending))
	}
	r.Units = append(r.Units, res)
	if live {
		if err := child(ctx, o, e.Home, "supervisor", "resume"); err != nil {
			r.fail(fmt.Sprintf("`hand supervisor resume` failed in %s (%v); run it there", e.Home, err))
			if stopErr == nil {
				return "switched; resume failed"
			}
		}
	}
	if stopErr != nil {
		return "failed: " + stopErr.Error()
	}
	return "switched"
}

func quiet(ctx context.Context, st *state.Store, c luvus.Client) (bool, error) {
	attempts, err := st.LiveAttempts(ctx)
	if err != nil || len(attempts) > 0 {
		return false, err
	}
	inputs, err := st.PendingSupervisorInputs(ctx)
	if err != nil || len(inputs) > 0 {
		return false, err
	}
	sup, live, err := st.LiveSupervisor(ctx)
	if err != nil || !live {
		return err == nil, err
	}
	ag, err := c.Explain(ctx, sup.PaneID)
	if err != nil {
		return false, nil
	}
	return ag.Status == "idle" || ag.Status == "done", nil
}

func child(ctx context.Context, o Options, home string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, o.Target, args...)
	cmd.Dir, cmd.Env, cmd.WaitDelay = home, append(append([]string{}, o.Env...), "HAND_HOME="+home), time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func swap(src, target string) error {
	fail := func(err error) error { return fmt.Errorf("update: cannot replace %s: %w", target, err) }
	in, err := os.Open(src)
	if err != nil {
		return fail(err)
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(target), ".hand.update-*")
	if err != nil {
		return fail(err)
	}
	_, err = io.Copy(tmp, in)
	if err == nil {
		err = tmp.Chmod(0o755)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), target)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return fail(err)
	}
	return nil
}

func Render(r Report) *toon.Doc {
	var d toon.Doc
	d.Field("status", r.Status)
	d.Field("from", r.From.Version+" "+r.From.Channel+" "+r.From.Commit)
	d.Field("to", r.To.Version+" "+r.To.Channel+" "+r.To.Commit)
	if r.PinFrom == r.PinTo {
		d.Field("luvus", r.PinTo+" kept")
	} else {
		d.Field("luvus", r.PinFrom+" -> "+r.PinTo)
	}
	d.List("backups", r.Backups)
	units := [][]string{}
	for _, u := range r.Units {
		units = append(units, []string{u.Name, u.Action, u.Result})
	}
	d.Rows("units", []string{"name", "action", "result"}, units)
	fleets := [][]string{}
	for _, f := range r.Fleets {
		fleets = append(fleets, []string{f.Name, f.Init, f.Luvus})
	}
	d.Rows("fleets", []string{"name", "init", "luvus"}, fleets)
	d.Help(r.Help...)
	return &d
}
