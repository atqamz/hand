package update

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

type Options struct {
	Target                          string
	From                            Build
	Channel                         string
	Check                           bool
	Root, HandBase, LuvusBase, Arch string
	Env                             []string
	Getenv                          func(string) string
	Now                             func() time.Time
	Server                          func(ctx context.Context, unit string) (luvus.Server, bool)
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
	tmp, err := os.MkdirTemp("", "hand-update-")
	if err != nil {
		return rep, err
	}
	defer os.RemoveAll(tmp)
	if rep.To, err = FetchHand(ctx, o.HandBase, o.Channel, o.Arch, tmp, o.Env); err != nil {
		return rep, err
	}
	if rep.To.Schema < state.SchemaVersion {
		return rep, fmt.Errorf("update: %s reads state schema %d, older than this fleet's %d", rep.To.Version, rep.To.Schema, state.SchemaVersion)
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
	fleets, err := fleet.List(o.Root)
	if err != nil {
		return rep, err
	}
	if rep.To.Channel == o.From.Channel && rep.To.Commit == o.From.Commit {
		rep.Status = "up to date"
		return rep, nil
	}
	if o.Check {
		rep.Status = "checked"
		return rep, nil
	}
	if rep.Backups, err = Backup(ctx, o.Root, o.Target, fleets, o.Now()); err != nil {
		return rep, err
	}
	if err := swap(rep.To.Path, o.Target); err != nil {
		return rep, err
	}
	units, err := Units(ctx, o.Env, o.Target)
	if err != nil {
		rep.fail(fmt.Sprintf("could not list Hand's units (%v); restart the board and watch units with `systemctl --user restart`", err))
	}
	for _, e := range fleets {
		if e.State != "ok" {
			continue
		}
		var watches []Unit
		for _, u := range units {
			if u.Command == "watch" && u.Active && filepath.Clean(u.Home) == filepath.Clean(e.Home) {
				watches = append(watches, u)
			}
		}
		for _, u := range watches {
			rep.unit(ctx, o, u.Name, "stop")
		}
		for _, u := range watches {
			rep.unit(ctx, o, u.Name, "start")
		}
	}
	for _, u := range units {
		if u.Command == "board" && u.Active {
			rep.unit(ctx, o, u.Name, "restart")
		}
	}
	for _, e := range fleets {
		fr := FleetResult{Name: e.Name, Init: "ok", Luvus: "kept"}
		if e.State != "ok" {
			fr.Name, fr.Init = e.ID, "skipped: "+e.State
		} else if err := child(ctx, o, e.Home, "init"); err != nil {
			fr.Init = "failed: " + err.Error()
			rep.fail(fmt.Sprintf("`hand init` failed in %s (%v); run it there", e.Home, err))
		}
		rep.Fleets = append(rep.Fleets, fr)
	}
	rep.Status = "updated"
	return rep, nil
}

func (r *Report) fail(help string) {
	r.Failed = true
	r.Help = append(r.Help, help)
}

func (r *Report) unit(ctx context.Context, o Options, name, action string) {
	res := UnitResult{Name: name, Action: action, Result: "ok"}
	if err := Systemctl(ctx, o.Env, action, name); err != nil {
		res.Result = "failed: " + err.Error()
		r.fail(fmt.Sprintf("`systemctl --user %s %s` failed (%v); run it again", action, name, err))
	}
	r.Units = append(r.Units, res)
}

func child(ctx context.Context, o Options, home string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, o.Target, args...)
	cmd.Dir, cmd.Env, cmd.WaitDelay = home, append(append([]string{}, o.Env...), "HAND_HOME="+home), time.Second
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
