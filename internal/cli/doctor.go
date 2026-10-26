package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/board"
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
		_, err := harness.LookPath(name, path)
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
	if runtime.GOOS == "linux" {
		if _, ok := luvus.UserManager(r.env.Environ()); ok {
			c.add("systemd", "ok", "")
		} else {
			c.add("systemd", "info", "no systemd user session: Hand starts the board, the watchers and Luvus as detached processes")
		}
	}
	root, err := r.rootDir()
	if err != nil {
		c.add("luvus pin", "fail", err.Error())
		c.add("fleet", "info", "the fleet checks were skipped: they need the secondhand folder")
	} else {
		pin, pinned := r.doctorPin(ctx, &d, &c, root, path)
		r.doctorFleet(ctx, &d, &c, root, pin, pinned, installed)
	}
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
	pin, had, err := luvus.LoadPin(root)
	switch {
	case err != nil:
		c.add("luvus pin", "fail", err.Error())
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
	} else {
		c.add("luvus pin", "ok", "")
	}
	return pin, true
}

func (r *runner) doctorFleet(ctx context.Context, d *toon.Doc, c *checks, root string, pin luvus.Pin, pinned bool, installed int) {
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
	doctorProjects(ctx, st, c)
	_, live, lerr := st.LiveSupervisor(ctx)
	if p, err := harness.LoadPolicy(r.home); err != nil {
		c.add(harness.PolicyFile, "fail", err.Error())
	} else {
		c.add(harness.PolicyFile, "ok", "")
		idle := installed == 0 && lerr == nil && !live && maps.Equal(p.Profiles, harness.StarterPolicy.Profiles)
		missing := false
		for _, name := range p.Names() {
			switch _, warn, err := checkedBin(r, p.Profiles[name]); {
			case err != nil && idle:
				c.add("profile "+name, "warn", err.Error())
				missing = true
			case err != nil:
				c.add("profile "+name, "fail", err.Error())
				missing = true
			case warn != "":
				c.add("profile "+name, "warn", warn)
			default:
				c.add("profile "+name, "ok", "")
			}
		}
		if missing {
			c.help = append(c.help, "The board searches the PATH it started with: restart it after you install a harness or change PATH")
		}
	}
	version, err := r.client(fleet.Session(f.ID)).Version(ctx)
	server, match := serverMatch(pin, pinned, version, err)
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
		c.add("phone", "info", "the board is not running, so its phone address is not checked")
	} else {
		c.add("board", "ok", "")
		r.doctorPhone(ctx, d, c, addr)
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

func (r *runner) doctorPhone(ctx context.Context, d *toon.Doc, c *checks, addr string) {
	link := r.phoneLink(ctx, addr)
	switch {
	case !link.Tailscale:
		c.add("phone", "info", "no tailscale answered; it is optional: install Tailscale to reach the board from a phone, or use another tunnel and type its address on the board's Phone page")
	case link.Funnel:
		c.add("phone", "fail", "tailscale funnel is on for this board, so the internet can reach it and the board refuses those requests; run `tailscale funnel reset`")
	case link.URL != "":
		d.Field("phone", link.URL)
		c.add("phone", "ok", "")
	case link.Problem == board.ServeAbsent:
		c.add("phone", "info", "tailscale serve does not proxy the board yet; open Phone in the board for the steps, or run `tailscale serve --bg "+link.Port+"`")
	default:
		c.add("phone", "warn", link.Problem+"; run `tailscale serve --bg "+link.Port+"`")
	}
}
