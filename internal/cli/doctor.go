package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/gitcfg"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/proc"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
	"github.com/atqamz/hand/internal/update"
)

func init() {
	commands["doctor"] = cmdDoctor
}

type checks struct {
	rows   [][]string
	failed int
	help   []string
}

func (c *checks) add(check, level, fix string) {
	c.rows = append(c.rows, []string{check, level, fix})
	if level == "fail" {
		c.failed++
	}
}

func cmdDoctor(r *runner, args []string) error {
	if _, err := parse(flags("doctor"), args, 0); err != nil {
		return err
	}
	var d toon.Doc
	var c checks
	ctx, path := r.ctx(), r.env.Getenv("PATH")
	doctorHand(&d, &c, path)
	r.doctorGit(ctx, &d, &c, path)
	installed := 0
	for _, name := range state.Harnesses {
		_, err := harness.Find(name, harness.EnvOf(r.env.Getenv))
		var batch *harness.BatchFileError
		if errors.As(err, &batch) {
			c.add("harness "+name, "warn", err.Error())
		}
		if err != nil {
			continue
		}
		installed++
		c.add("harness "+name, "ok", "")
		c.add("models "+name, "info", r.modelSummary(name))
	}
	if installed == 0 {
		c.add("harness", "warn", "install one of "+strings.Join(state.Harnesses[:len(state.Harnesses)-1], ", ")+" or "+state.Harnesses[len(state.Harnesses)-1]+", or add its folder to PATH")
		c.help = append(c.help, "The board searches the PATH it started with: restart it after you install a harness or change PATH")
	}
	if runtime.GOOS == "linux" {
		if _, ok := luvus.UserManager(r.env.Environ()); ok {
			c.add("systemd", "ok", "")
		} else {
			c.add("systemd", "info", "no systemd user session: Hand starts the board, the watchers and Luvus as detached processes")
		}
	}
	r.doctorDesktop(ctx, &d, &c, path)
	root, err := r.rootDir()
	if err != nil {
		c.add("luvus pin", "fail", err.Error())
		c.add("fleet", "info", "the fleet checks were skipped: they need the secondhand folder")
	} else {
		pin, pinned := r.doctorPin(ctx, &d, &c, root, path)
		r.doctorIroh(ctx, &d, &c, root, path)
		r.doctorFleet(ctx, &d, &c, root, pin, pinned)
		r.doctorWatchUnits(ctx, &c, root)
	}
	r.doctorSkills(&c)
	d.Rows("checks", []string{"check", "state", "fix"}, c.rows)
	d.Help(c.help...)
	if err := r.print(&d); err != nil {
		return err
	}
	if c.failed > 0 {
		return fmt.Errorf("doctor: %d check(s) failed; follow their fix lines", c.failed)
	}
	return nil
}

func doctorHand(d *toon.Doc, c *checks, path string) {
	d.Field("version", Version)
	d.Field("channel", Channel)
	exe, err := os.Executable()
	if err == nil {
		exe, err = update.Target(exe)
	}
	if err != nil {
		c.add("hand", "fail", err.Error())
		return
	}
	d.Field("path", exe)
	found, err := harness.LookPath(filepath.Base(exe), path)
	var a, b os.FileInfo
	if err == nil {
		if a, err = os.Stat(found); err == nil {
			b, err = os.Stat(exe)
		}
	}
	switch {
	case err != nil || !os.SameFile(a, b):
		c.add("hand", "warn", "PATH does not lead to this binary; put "+filepath.Dir(exe)+" on PATH")
	case Channel == "source":
		c.add("hand", "info", "a source build: `hand update` needs --channel edge or --channel stable")
	default:
		c.add("hand", "ok", "")
	}
}

func (r *runner) doctorGit(ctx context.Context, d *toon.Doc, c *checks, path string) {
	bin, err := harness.LookPath("git", path)
	if err != nil {
		c.add("git", "fail", "install git, or add its folder to PATH")
		return
	}
	out, err := r.probe(ctx, bin, "--version")
	if err != nil {
		c.add("git", "fail", fmt.Sprintf("%s --version failed: %v", bin, err))
		return
	}
	d.Field("git", strings.TrimPrefix(out, "git version "))
	if runtime.GOOS == "windows" {
		if on, _ := r.probe(ctx, bin, "config", "--type=bool", "--get", "core.longpaths"); on != "true" {
			c.add("git", "warn", "core.longpaths is off; run `git config --global core.longpaths true`")
			return
		}
	}
	c.add("git", "ok", "")
}

func (r *runner) probe(ctx context.Context, bin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env, cmd.WaitDelay = r.env.Environ(), time.Second
	proc.NoWindow(cmd)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func (r *runner) doctorPin(ctx context.Context, d *toon.Doc, c *checks, root, path string) (luvus.Pin, bool) {
	pin, had, err := fleet.LoadPin(root, r.home)
	switch {
	case err != nil:
		c.add("luvus pin", "fail", err.Error())
		return pin, false
	case !had && r.home == "":
		c.add("luvus pin", "info", "a Luvus pin belongs to a fleet; run `hand doctor` inside one to check it")
		return pin, false
	case !had:
		if _, err := harness.LookPath("luvus", path); err != nil {
			c.add("luvus pin", "fail", "no Luvus is pinned or on PATH; run `hand luvus pin`, which downloads the tested version")
		} else {
			c.add("luvus pin", "warn", "nothing is pinned, so Hand runs the luvus on PATH; pin it with `hand luvus pin`")
		}
		return pin, false
	}
	d.Field("luvus", pinLabel(pin, had))
	if err := pin.Verify(); err != nil {
		c.add("luvus pin", "fail", err.Error())
	} else if _, err := luvus.BinaryVersion(ctx, pin.Path, r.env.Environ()); err != nil {
		c.add("luvus pin", "fail", err.Error()+"; re-pin with `hand luvus pin`")
	} else if r.home != "" && !within(pin.Path, fleet.LuvusStore(r.home)) {
		c.add("luvus pin", "warn", "the pin is still the machine copy "+pin.Path+"; any hand command in this fleet, or `hand init`, copies it into "+fleet.LuvusStore(r.home))
	} else {
		c.add("luvus pin", "ok", "")
	}
	return pin, true
}

func (r *runner) doctorLayout(c *checks, root, id string) {
	c.add("machine-local", "info", fmt.Sprintf("this machine keeps worktrees/, index/, board.* and iroh/ (the Board's pid, address, peer secret and phone key), models/ (keeps the fleets from each refreshing the same lists) and backups/hand.* (old hand binaries) in %s; the fleet keeps luvus/%s-%s, backups/ and cache/models", root, runtime.GOOS, runtime.GOARCH))
	points := fleet.RestorePoints(root, id, r.home)
	if len(points) == 0 {
		c.add("restore points", "info", "no hand.db backup yet; `hand update` and the watcher make them")
	} else {
		c.add("restore points", "info", fmt.Sprintf("%d hand.db backups, newest %s", len(points), points[0]))
	}
	var old []string
	_, own, _ := luvus.LoadFleetPin(fleet.LuvusStore(r.home))
	if _, err := os.Stat(filepath.Join(fleet.OldLuvusStore(root), "pin.json")); err == nil && own {
		old = append(old, "luvus/ (a running Luvus server may still run from it)")
	}
	if des, err := os.ReadDir(fleet.OldBackups(root, id)); err == nil && len(des) > 0 {
		old = append(old, "backups/"+id+"/")
	}
	if len(old) > 0 {
		c.add("old machine copies", "info", "the machine still holds "+strings.Join(old, " and ")+" in "+root+"; Hand copies them into the fleet and never deletes them, so delete them yourself once no Luvus server runs from the machine copy and you no longer need the older restore points")
	}
}

func (r *runner) doctorFleet(ctx context.Context, d *toon.Doc, c *checks, root string, pin luvus.Pin, pinned bool) {
	if err := r.needHome(); err != nil {
		level := "fail"
		if r.home == "" && r.homeErr == nil {
			level = "info"
		}
		c.add("fleet", level, err.Error()+"; the fleet checks were skipped")
		return
	}
	st, err := state.Open(r.dbPath(), r.env.Now)
	if err != nil {
		c.add("schema", "fail", err.Error())
		return
	}
	defer st.Close()
	c.add("schema", "ok", "")
	f, err := st.Fleet(ctx)
	if err != nil {
		c.add("fleet", "fail", err.Error())
		return
	}
	if _, err := fleet.Home(root, f.ID); err != nil {
		c.add("fleet", "fail", err.Error()+"; run `hand init` here")
	} else if from, err := fleet.Moved(root, f.ID, r.home); err != nil {
		c.add("fleet", "fail", err.Error())
	} else if from != "" {
		c.add("fleet", "fail", "fleet "+f.ID+" is registered at "+from+"; run `hand init` here to adopt this place")
	} else {
		c.add("fleet", "ok", "")
	}
	if runtime.GOOS != "windows" {
		switch enforced, err := modesEnforced(r.home); {
		case err != nil:
			c.add("modes", "warn", "could not test whether this filesystem enforces file modes: "+err.Error())
		case !enforced:
			c.add("modes", "warn", "this filesystem does not enforce file modes, so board.token and hand.db are readable by anyone who can mount it; encrypt the volume")
		default:
			c.add("modes", "ok", "")
		}
	}
	r.doctorHost(c)
	r.doctorLayout(c, root, f.ID)
	doctorProjects(ctx, st, c)
	_, live, lerr := st.LiveSupervisor(ctx)
	if _, err := harness.LoadPolicy(r.home); err != nil {
		c.add(harness.PolicyFile, "fail", err.Error())
	} else {
		c.add(harness.PolicyFile, "ok", "")
	}
	version, err := r.client(fleet.Session(f.ID)).Version(ctx)
	server, match := luvus.Match(pin, pinned, version, err)
	d.Field("server", server)
	switch match {
	case "yes":
		c.add("luvus server", "ok", "")
	case "not running":
		c.add("luvus server", "info", "not running; the next hand command that needs it starts the pinned copy")
	case "no":
		c.add("luvus server", "warn", serverSwitchHint)
	default:
		c.add("luvus server", "warn", "its version cannot be compared with the pin; see `hand luvus show`")
	}
	addr, proof, err := askBoard(ctx, root, f.ID, rand.Text())
	if _, herr := hex.DecodeString(proof); err == nil && (herr != nil || len(proof) != 64) {
		err = fmt.Errorf("no board for this fleet answers at %s; %s", addr, boardHint)
	}
	if err != nil {
		c.add("board", "warn", err.Error())
	} else {
		c.add("board", "ok", "")
	}
	held, err := r.watchHeld()
	if err == nil {
		err = lerr
	}
	switch {
	case err != nil:
		c.add("watcher", "warn", err.Error())
	case held:
		c.add("watcher", "ok", "")
	case live:
		c.add("watcher", "warn", "a supervisor runs but no watcher does; a running board starts one within 30 seconds, or run `hand watch`")
	default:
		c.add("watcher", "info", "none runs, and none is needed until a supervisor starts")
	}
}

func doctorProjects(ctx context.Context, st *state.Store, c *checks) {
	projects, err := st.Projects(ctx)
	if err != nil {
		c.add("projects", "fail", err.Error())
		return
	}
	for _, p := range projects {
		check := "project " + p.Name
		if p.Settings == "" {
			c.add(check, "info", "its git settings are recorded the first time Hand runs git in it")
			continue
		}
		var changed gitcfg.ChangedError
		switch err := (gitcfg.Repo{Project: p.Name, Dir: p.Repo, Recorded: p.Settings}).Check(ctx); {
		case errors.As(err, &changed):
			c.add(check, "fail", err.Error())
		case err != nil:
			c.add(check, "warn", err.Error())
		default:
			c.add(check, "ok", "")
		}
	}
}

func (r *runner) doctorWatchUnits(ctx context.Context, c *checks, root string) {
	if runtime.GOOS != "linux" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	target, err := update.Target(exe)
	if err != nil {
		return
	}
	problems, err := update.StaleWatchUnits(ctx, r.env.Environ(), target, root)
	if err != nil {
		c.add("watch unit", "info", "could not read the systemd user units: "+err.Error())
		return
	}
	for _, p := range problems {
		why := p.Name + " names " + p.Home
		switch p.Reason {
		case "relative":
			why += " (relative HAND_HOME)"
		case "missing":
			why += " (missing)"
		case "unregistered":
			why += " (no registered fleet)"
		case "replaced":
			why += " (the watcher runs as the transient unit)"
		}
		fix := "run `hand update` to remove it"
		if !p.Fixable {
			fix = "Hand did not write it; run `systemctl --user disable --now " + p.Name + "`"
		}
		c.add("watch unit", "warn", why+"; "+fix)
	}
}

var modesEnforced = probeModes

// probeModes reads the permission bits a new file reports. A share that reports 0600 without enforcing it (CIFS noperm, some exFAT fmask layouts) passes, so this is a hint, not a proof.
func probeModes(dir string) (bool, error) {
	f, err := os.CreateTemp(dir, ".modes-*")
	if err != nil {
		return false, err
	}
	name := f.Name()
	_ = f.Close()
	defer os.Remove(name)
	info, err := os.Stat(name)
	if err != nil {
		return false, err
	}
	return info.Mode().Perm()&0o077 == 0, nil
}
