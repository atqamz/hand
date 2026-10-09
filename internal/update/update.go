package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/flock"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/proc"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

type Options struct {
	Target                              string
	From                                Build
	Channel                             string
	Check, KeepLuvus                    bool
	Root, HandBase, LuvusBase, OS, Arch string
	Env                                 []string
	Getenv                              func(string) string
	Now                                 func() time.Time
	Server                              func(ctx context.Context, unit string) (luvus.Server, bool)
	Hold                                func()
	Watch, Board                        func(ctx context.Context, home string) error
	Cgroup                              string
	Stop                                func(pid int, marker string) error
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
	journal        journal
}

func Target(exe string) (string, error) {
	return filepath.EvalSymlinks(exe)
}

func lockFile(root string) (*os.File, bool, error) {
	f, err := os.OpenFile(filepath.Join(root, "update.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	ok, err := flock.Lock(f, false)
	if !ok || err != nil {
		_ = f.Close()
	}
	return f, ok, err
}

func Running(root string) (bool, error) {
	f, ok, err := lockFile(root)
	if ok {
		_ = flock.Release(f)
	}
	return !ok && err == nil, err
}

func Run(ctx context.Context, o Options) (Report, error) {
	rep := Report{From: o.From}
	if o.Channel != "edge" && o.Channel != "stable" {
		return rep, fmt.Errorf("%w: update: --channel must be edge or stable", state.ErrInvalid)
	}
	if !o.Check {
		lock, ok, err := lockFile(o.Root)
		if err != nil {
			return rep, err
		}
		if !ok {
			return rep, fmt.Errorf("%w: update: another hand update is running; wait for it to finish", state.ErrConflict)
		}
		defer flock.Release(lock)
	}
	held := false
	hold := func() {
		if o.Hold != nil && !held {
			o.Hold()
		}
		held = true
	}
	if err := rep.finishJournal(ctx, o, hold); err != nil {
		return rep, err
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
	if current && !repin && (o.KeepLuvus || !slices.ContainsFunc(fleets, func(e fleet.Entry) bool { return e.State == "ok" && !matches(e) })) {
		if rep.Status != "repaired" {
			rep.Status = "up to date"
		}
		return rep, nil
	}
	if o.Check {
		rep.cycle(ctx, o, fleets, repin, matches)
		rep.Status = "checked"
		return rep, nil
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
	for _, e := range fleets {
		if e.State == "ok" {
			rep.journal.Init = append(rep.journal.Init, e.Home)
		}
	}
	if !current {
		if rep.Backups, err = Backup(ctx, o.Root, o.Target, fleets, o.Now()); err != nil {
			return rep, err
		}
	}
	if err := rep.journal.save(o.Root); err != nil {
		return rep, fmt.Errorf("update: cannot write %s: %w", journalPath(o.Root), err)
	}
	if !current {
		if err := swap(rep.To.Path, o.Target); err != nil {
			_ = os.Remove(journalPath(o.Root))
			return rep, err
		}
		if err := Prune(o.Root, fleets); err != nil {
			rep.Help = append(rep.Help, fmt.Sprintf("could not remove old backups (%v)", err))
		}
	}
	rep.cycle(ctx, o, fleets, repin, matches)
	for i, e := range fleets {
		if e.State != "ok" {
			continue
		}
		if err := child(ctx, o, e.Home, "init"); err != nil {
			rep.Fleets[i].Init = "failed: " + err.Error()
			rep.fail(fmt.Sprintf("`hand init` failed in %s (%v); run it there", e.Home, err))
		}
		rep.unrecord(o, func(j *journal) { j.Init = without(j.Init, e.Home) })
	}
	if err := os.Remove(journalPath(o.Root)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		rep.Help = append(rep.Help, fmt.Sprintf("could not remove %s (%v)", journalPath(o.Root), err))
	}
	rep.Status = "updated"
	return rep, nil
}

func (r *Report) cycle(ctx context.Context, o Options, fleets []fleet.Entry, repin bool, matches func(fleet.Entry) bool) {
	init := "ok"
	if o.Check {
		init = "would run"
	}
	var homes []string
	for _, e := range fleets {
		fr := FleetResult{Name: e.Name, Init: init, Luvus: "kept"}
		if e.State != "ok" {
			fr.Name, fr.Init = e.ID, "skipped: "+e.State
			r.Fleets = append(r.Fleets, fr)
			continue
		}
		homes = append(homes, e.Home)
		stopped, did := r.stopPID(o, "watch "+e.Name, filepath.Join(e.Home, "watch.pid"), func(j *journal) { j.Watcher = append(j.Watcher, e.Home) })
		if !matches(e) || o.Check && repin {
			if stopped && !o.KeepLuvus {
				fr.Luvus = r.switchLuvus(ctx, o, e)
			} else {
				fr.Luvus = "pending"
				r.Help = append(r.Help, pendingLine(e))
			}
			if o.Check && fr.Luvus != "would switch" {
				fr.Luvus = "would stay pending"
			}
		}
		if did {
			if stopped {
				r.start(ctx, o, "watch "+e.Home, []string{e.Home}, o.Watch)
			}
			r.unrecord(o, func(j *journal) { j.Watcher = without(j.Watcher, e.Home) })
		}
		r.Fleets = append(r.Fleets, fr)
	}
	if len(homes) == 0 {
		return
	}
	if stopped, did := r.stopPID(o, "board", filepath.Join(o.Root, "board.pid"), func(j *journal) { j.Board = append(j.Board, homes...) }); did {
		if stopped {
			r.start(ctx, o, "board", homes, o.Board)
		}
		r.unrecord(o, func(j *journal) { j.Board = nil })
	}
}

func (r *Report) fail(help string) {
	r.Failed = true
	r.Help = append(r.Help, help)
}

func (r *Report) start(ctx context.Context, o Options, name string, homes []string, ensure func(context.Context, string) error) {
	res := UnitResult{Name: name, Action: "start", Result: "ok"}
	if o.Check {
		res.Result = "would run"
	} else {
		var err error
		for _, home := range homes {
			if err = ensure(ctx, home); err == nil {
				break
			}
		}
		if err != nil {
			res.Result = "failed: " + err.Error()
			r.fail(fmt.Sprintf("could not start %s (%v); run `hand %s` for it", name, err, strings.Fields(name)[0]))
		}
	}
	r.Units = append(r.Units, res)
}

func (r *Report) stopPID(o Options, name, path string, mark func(*journal)) (stopped, did bool) {
	e, live := livePID(path)
	if !live {
		return true, false
	}
	res := UnitResult{Name: name, Action: "stop", Result: "ok"}
	if e.exe != "" && e.exe != o.Target {
		res.Result = "skipped: runs " + e.exe
		r.Units = append(r.Units, res)
		r.Help = append(r.Help, fmt.Sprintf("%s runs %s, not %s, so it was left running", name, e.exe, o.Target))
		return false, false
	}
	if o.Check {
		res.Result = "would run"
		r.Units = append(r.Units, res)
		return true, true
	}
	if err := r.record(o, mark); err != nil {
		r.fail(fmt.Sprintf("could not write %s (%v), so %s keeps running and its Luvus server is left alone", journalPath(o.Root), err, name))
		return false, false
	}
	err := o.Stop(e.pid, e.marker)
	if err != nil {
		res.Result = "failed: " + err.Error()
		r.fail(fmt.Sprintf("could not stop %s (%v); stop process %d", name, err, e.pid))
	} else {
		_ = os.Remove(path)
	}
	r.Units = append(r.Units, res)
	return err == nil, true
}

var hostOS = runtime.GOOS

func pendingLine(e fleet.Entry) string {
	return pendingLineFor(hostOS, e)
}

func pendingLineFor(goos string, e fleet.Entry) string {
	if goos != "linux" {
		return "At a quiet time: stop the fleet's Luvus server (this ends every live pane), then `hand supervisor resume` in " + e.Home
	}
	return "At a quiet time: `systemctl --user stop " + fleet.LuvusUnit(e.ID) + ".service`, then `hand supervisor resume` in " + e.Home
}

func (o Options) inside(e fleet.Entry) bool {
	return strings.Contains(o.Cgroup, "/"+fleet.LuvusUnit(e.ID)+".service")
}

func (o Options) switchable(ctx context.Context, e fleet.Entry) (live, ok bool, other string, err error) {
	if o.inside(e) {
		return false, false, "", nil
	}
	st, err := state.Open(filepath.Join(e.Home, "hand.db"), o.Now)
	if err != nil {
		return false, false, "", err
	}
	defer st.Close()
	if _, live, err = st.LiveSupervisor(ctx); err != nil {
		return false, false, "", err
	}
	ok, other, err = quiet(ctx, st, luvus.Client{Socket: luvus.SocketPath(o.Getenv, fleet.Session(e.ID))}, e.Home)
	return live, ok, other, err
}

func (r *Report) switchLuvus(ctx context.Context, o Options, e fleet.Entry) string {
	unit := fleet.LuvusUnit(e.ID) + ".service"
	pending := pendingLine(e)
	if hostOS != "linux" {
		r.Help = append(r.Help, pending)
		return "pending"
	}
	live, ok, other, err := o.switchable(ctx, e)
	switch {
	case err != nil:
		r.fail(fmt.Sprintf("could not read %s (%v). %s", e.Home, err, pending))
		return "failed: " + err.Error()
	case o.inside(e):
		r.Help = append(r.Help, "This hand update runs inside "+fleet.LuvusUnit(e.ID)+", so it leaves that server alone; run it from outside the fleet's Luvus panes. "+pending)
		return "pending"
	case other != "":
		r.Help = append(r.Help, other+" "+pending)
		return "pending"
	case !ok:
		r.Help = append(r.Help, pending)
		return "pending"
	}
	if o.Check {
		r.Units = append(r.Units, UnitResult{Name: unit, Action: "stop", Result: "would run"})
		return "would switch"
	}
	if live {
		if err := r.record(o, func(j *journal) { j.Supervisor = append(j.Supervisor, e.Home) }); err != nil {
			r.Help = append(r.Help, fmt.Sprintf("could not write %s (%v). %s", journalPath(o.Root), err, pending))
			return "pending"
		}
		defer r.unrecord(o, func(j *journal) { j.Supervisor = without(j.Supervisor, e.Home) })
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

func quiet(ctx context.Context, st *state.Store, c luvus.Client, home string) (bool, string, error) {
	attempts, err := st.LiveAttempts(ctx)
	if err != nil || len(attempts) > 0 {
		return false, "", err
	}
	inputs, err := st.PendingSupervisorInputs(ctx)
	if err != nil || len(inputs) > 0 {
		return false, "", err
	}
	sup, live, err := st.LiveSupervisor(ctx)
	if err != nil {
		return false, "", err
	}
	terms, err := c.Inventory(ctx)
	if err != nil {
		return false, "", nil
	}
	listed := false
	for _, t := range terms {
		switch {
		case live && t.ServerGeneration == sup.ServerGeneration && t.TerminalID == sup.TerminalID && t.PaneID == sup.PaneID:
			listed = true
		case initialShell(t, home):
		case rootAlive(t.Root):
			return false, "Pane " + t.PaneID + " (" + t.CWD + ") is not Hand's, and stopping the Luvus server ends it.", nil
		}
	}
	if !live {
		return true, "", nil
	}
	if !listed {
		return false, "", nil
	}
	ag, err := c.Explain(ctx, sup.PaneID)
	if err != nil {
		return false, "", nil
	}
	return ag.Status == "idle" || ag.Status == "done", "", nil
}

func initialShell(t luvus.Terminal, home string) bool {
	if t.Label != "" || t.PaneID != "1" {
		return false
	}
	a, err := os.Stat(t.CWD)
	if err != nil {
		return false
	}
	b, err := os.Stat(filepath.Join(home, "luvus"))
	return err == nil && os.SameFile(a, b)
}

func rootAlive(r luvus.Root) bool {
	m, err := luvus.ProcStartMarker(r.PID)
	if err != nil {
		return !errors.Is(err, fs.ErrNotExist)
	}
	return m == r.StartMarker
}

func child(ctx context.Context, o Options, home string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, o.Target, args...)
	cmd.Dir, cmd.Env, cmd.WaitDelay = home, append(append([]string{}, o.Env...), "HAND_HOME="+home), time.Second
	proc.NewGroup(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func stage(src, target string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(target), ".hand.update-*")
	if err != nil {
		return "", err
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
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
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
