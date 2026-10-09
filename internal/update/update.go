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
	Check, KeepLuvus, Auto              bool
	Root, HandBase, LuvusBase, OS, Arch string
	Env                                 []string
	Getenv                              func(string) string
	Now                                 func() time.Time
	Client                              func(e fleet.Entry) luvus.Client
	Hold                                func()
	Watch, Board                        func(ctx context.Context, home string) error
	Stop, Kill                          func(pid int, marker string) error
	Start                               func(ctx context.Context, home string) error
	Version                             func(ctx context.Context, e fleet.Entry) (string, error)
	Sleep                               func(ctx context.Context, d time.Duration) error
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
	server := func(e fleet.Entry) string { return o.serverState(ctx, e, pin, pinned) }
	current := rep.To.Channel == o.From.Channel && rep.To.Commit == o.From.Commit
	if current && !repin {
		var stale []fleet.Entry
		if !o.KeepLuvus {
			stale = slices.DeleteFunc(slices.Clone(fleets), func(e fleet.Entry) bool { return e.State != "ok" || server(e) != "stale" })
		}
		if len(stale) > 0 && !o.Check {
			hold()
		}
		if len(stale) > 0 {
			rep.cycle(ctx, o, stale, false, repin, server)
		}
		if len(stale) > 0 && !o.Check {
			rep.clearJournal(o)
		}
		switch {
		case o.Check && len(stale) > 0:
			rep.Status = "checked"
		case rep.Status != "repaired":
			rep.Status = "up to date"
		}
		return rep, nil
	}
	if o.Check {
		rep.cycle(ctx, o, fleets, true, repin, server)
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
	rep.cycle(ctx, o, fleets, true, repin, server)
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
	rep.clearJournal(o)
	rep.Status = "updated"
	return rep, nil
}

func (r *Report) clearJournal(o Options) {
	if err := os.Remove(journalPath(o.Root)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		r.Help = append(r.Help, fmt.Sprintf("could not remove %s (%v)", journalPath(o.Root), err))
	}
}

func (r *Report) cycle(ctx context.Context, o Options, fleets []fleet.Entry, full, repin bool, server func(fleet.Entry) string) {
	init := "ok"
	switch {
	case o.Check:
		init = "would run"
	case !full:
		init = "skipped: current"
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
		sv := server(e)
		switchable := sv == "stale" || o.Check && repin && sv != "not running" && sv != "unknown"
		why := ""
		if switchable && !o.KeepLuvus {
			why = o.ready(ctx, e, r.PinTo)
		}
		var stopped, did bool
		if full || switchable && why == "" && !o.KeepLuvus {
			stopped, did = r.stopPID(o, "watch "+e.Name, filepath.Join(e.Home, "watch.pid"), func(j *journal) { j.Watcher = append(j.Watcher, e.Home) })
		}
		switch {
		case sv == "not running":
			fr.Luvus = sv
		case sv == "unknown":
			fr.Luvus = sv
			reason := "does not report its version"
			if o.Getenv("HAND_LUVUS_SOCKET") != "" {
				reason = "cannot be told apart from the others while HAND_LUVUS_SOCKET is set"
			}
			r.Help = append(r.Help, "The Luvus server of "+e.Name+" "+reason+", so `hand update` leaves it alone")
		case switchable:
			switch {
			case o.KeepLuvus:
				why = "--keep-luvus"
			case why == "" && !stopped:
				why = "the watcher is still running"
			}
			switch {
			case why != "":
				fr.Luvus = "pending: " + why
			case o.Check:
				r.Units = append(r.Units, UnitResult{Name: "luvus " + e.Name, Action: "stop", Result: "would run"})
				fr.Luvus = "would switch"
			default:
				fr.Luvus = r.switchLuvus(ctx, o, e)
			}
			if strings.HasPrefix(fr.Luvus, "pending") {
				if !slices.Contains(r.Help, pendingLine) {
					r.Help = append(r.Help, pendingLine)
				}
				if o.Check {
					fr.Luvus = "would stay " + fr.Luvus
				}
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
	if len(homes) == 0 || !full {
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

const (
	pendingLine = "`hand update` switches a pending fleet once it is quiet and settled; `hand board --update-every` tries again by itself"
	settledFor  = 15 * time.Minute
	stillFor    = 30 * time.Second
)

func (o Options) client(e fleet.Entry) luvus.Client {
	if o.Client != nil {
		return o.Client(e)
	}
	return luvus.Client{Socket: luvus.SocketPath(o.Getenv, fleet.Session(e.ID))}
}

func (o Options) running(ctx context.Context, e fleet.Entry) (string, error) {
	if o.Version != nil {
		return o.Version(ctx, e)
	}
	return o.client(e).Version(ctx)
}

func (o Options) serverState(ctx context.Context, e fleet.Entry, pin luvus.Pin, pinned bool) string {
	if o.Getenv("HAND_LUVUS_SOCKET") != "" {
		return "unknown"
	}
	version, err := o.running(ctx, e)
	switch {
	case errors.Is(err, luvus.ErrUnreachable):
		return "not running"
	case err != nil || version == "":
		return "unknown"
	case pinned && version != pin.Version:
		return "stale"
	}
	return "match"
}

func (o Options) inside(e fleet.Entry) bool {
	return o.Getenv("LUVUS_SESSION") == fleet.Session(e.ID)
}

func (o Options) ready(ctx context.Context, e fleet.Entry, to string) string {
	if o.inside(e) {
		return "this update runs inside the Luvus session of " + e.Name
	}
	st, err := state.Open(filepath.Join(e.Home, "hand.db"), o.Now)
	if err != nil {
		return "cannot read hand.db: " + err.Error()
	}
	defer st.Close()
	if why := o.settled(ctx, st); why != "" {
		return why
	}
	if o.Auto {
		failed, err := st.SwitchFailed(ctx, to)
		if err != nil {
			return err.Error()
		}
		if failed {
			return "an earlier switch to " + to + " failed; run `hand update` to try again"
		}
	}
	return quiet(ctx, st, o.client(e), e.Home)
}

func (o Options) settled(ctx context.Context, st *state.Store) string {
	last, err := st.RecentEvents(ctx, 1)
	if err != nil {
		return err.Error()
	}
	if len(last) == 0 {
		return ""
	}
	if at, err := time.Parse(time.RFC3339Nano, last[0].At); err != nil || o.Now().Sub(at) < settledFor {
		return "the fleet recorded an event within the last 15 minutes"
	}
	return ""
}

func (o Options) still(ctx context.Context, st *state.Store, c luvus.Client, home string, sup state.Supervisor, live bool) string {
	if live {
		revision := func() (int64, error) {
			s, err := c.Read(ctx, sup.PaneID, luvus.ScreenLines)
			return s.ContentRevision, err
		}
		before, err := revision()
		if err == nil {
			err = o.pause(ctx)
		}
		var after int64
		if err == nil {
			after, err = revision()
		}
		switch {
		case err != nil:
			return "cannot watch the supervisor's screen: " + err.Error()
		case before != after:
			return "the supervisor's screen changed"
		}
	}
	if why := o.settled(ctx, st); why != "" {
		return why
	}
	return quiet(ctx, st, c, home)
}

func (o Options) pause(ctx context.Context) error {
	if o.Sleep != nil {
		return o.Sleep(ctx, stillFor)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(stillFor):
		return nil
	}
}

func (o Options) stopServer(ctx context.Context, e fleet.Entry) error {
	pin, _, err := luvus.LoadPin(o.Root)
	if err == nil {
		err = pin.Verify()
	}
	if err != nil {
		return err
	}
	_, dir, err := luvus.Address(ctx, pin.Path, fleet.Session(e.ID), o.Env)
	if err != nil {
		return err
	}
	return o.client(e).StopServer(ctx, dir, o.Kill)
}

func (o Options) startServer(ctx context.Context, e fleet.Entry, to string) error {
	if err := o.Start(ctx, e.Home); err != nil {
		return err
	}
	got, err := o.client(e).Version(ctx)
	if err == nil && got != to {
		err = fmt.Errorf("the server came back as %q, not the pinned %s", got, to)
	}
	return err
}

func (r *Report) resume(ctx context.Context, o Options, home string) error {
	if live, err := supervisorLive(ctx, home, o.Now); err == nil && live {
		return nil
	}
	err := child(ctx, o, home, "supervisor", "resume")
	if err != nil {
		r.fail(fmt.Sprintf("`hand supervisor resume` failed in %s (%v); run it there", home, err))
	}
	return err
}

func (r *Report) failSwitch(ctx context.Context, st *state.Store, e fleet.Entry, reason string) string {
	reason = strings.Join(strings.Fields(reason), " ")
	r.fail(fmt.Sprintf("the Luvus switch of %s failed (%s); the board's timer will not try again, but `hand update` will", e.Name, reason))
	if err := st.NoteLuvus(ctx, "failed", r.PinTo+": "+reason); err != nil {
		r.Help = append(r.Help, fmt.Sprintf("could not record luvus.failed in %s (%v)", e.Home, err))
	}
	return "failed: " + reason
}

func (r *Report) switchLuvus(ctx context.Context, o Options, e fleet.Entry) string {
	st, err := state.Open(filepath.Join(e.Home, "hand.db"), o.Now)
	if err != nil {
		return "pending: cannot read hand.db: " + err.Error()
	}
	defer st.Close()
	c := o.client(e)
	sup, live, err := st.LiveSupervisor(ctx)
	if err != nil {
		return "pending: " + err.Error()
	}
	if why := o.still(ctx, st, c, e.Home, sup, live); why != "" {
		return "pending: " + why
	}
	from, _ := o.running(ctx, e)
	if live {
		if err := r.record(o, func(j *journal) { j.Supervisor = append(j.Supervisor, e.Home) }); err != nil {
			r.Help = append(r.Help, fmt.Sprintf("could not write %s (%v)", journalPath(o.Root), err))
			return "pending: cannot write the update journal"
		}
		defer r.unrecord(o, func(j *journal) { j.Supervisor = without(j.Supervisor, e.Home) })
		if err := child(ctx, o, e.Home, "supervisor", "stop"); err != nil {
			_ = r.resume(ctx, o, e.Home)
			return r.failSwitch(ctx, st, e, "`hand supervisor stop` failed: "+err.Error())
		}
		if why := quiet(ctx, st, c, e.Home); why != "" {
			_ = r.resume(ctx, o, e.Home)
			return "pending: " + why
		}
	}
	res := UnitResult{Name: "luvus " + e.Name, Action: "stop", Result: "ok"}
	err = o.stopServer(ctx, e)
	if err != nil {
		res.Result = "failed: " + err.Error()
	}
	r.Units = append(r.Units, res)
	if err == nil {
		err = o.startServer(ctx, e, r.PinTo)
	}
	resumed := !live || r.resume(ctx, o, e.Home) == nil
	switch {
	case err != nil:
		return r.failSwitch(ctx, st, e, err.Error())
	case !resumed:
		return "switched; resume failed"
	}
	if err := st.NoteLuvus(ctx, "switched", from+" -> "+r.PinTo); err != nil {
		r.Help = append(r.Help, fmt.Sprintf("could not record luvus.switched in %s (%v)", e.Home, err))
	}
	return "switched"
}

func quiet(ctx context.Context, st *state.Store, c luvus.Client, home string) string {
	attempts, err := st.LiveAttempts(ctx)
	if err != nil {
		return err.Error()
	}
	if len(attempts) > 0 {
		return "a worker attempt is live"
	}
	inputs, err := st.PendingSupervisorInputs(ctx)
	if err != nil {
		return err.Error()
	}
	if len(inputs) > 0 {
		return "a message waits for the supervisor"
	}
	sup, live, err := st.LiveSupervisor(ctx)
	if err != nil {
		return err.Error()
	}
	terms, err := c.Inventory(ctx)
	if err != nil {
		return "cannot list the Luvus panes: " + err.Error()
	}
	listed := false
	for _, t := range terms {
		switch {
		case live && t.ServerGeneration == sup.ServerGeneration && t.TerminalID == sup.TerminalID && t.PaneID == sup.PaneID:
			listed = true
		case initialShell(t, home):
		case rootAlive(t.Root):
			return "pane " + t.PaneID + " (" + t.CWD + ") is not Hand's, and stopping the Luvus server ends it"
		}
	}
	if !live {
		return ""
	}
	if !listed {
		return "the supervisor's pane is not in the Luvus inventory"
	}
	ag, err := c.Explain(ctx, sup.PaneID)
	if err != nil {
		return "cannot read the supervisor's state: " + err.Error()
	}
	if ag.Status != "idle" && ag.Status != "done" {
		return "the supervisor is " + ag.Status
	}
	return ""
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
