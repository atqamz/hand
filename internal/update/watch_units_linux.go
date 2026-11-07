//go:build linux

package update

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
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/proc"
)

type installedWatchUnit struct {
	name, description, home, command, binary, fragment string
}

func (r *Report) retireWatchUnits(ctx context.Context, o Options, fleets []fleet.Entry) {
	bin, env, ok := userSystemctl(o.Env)
	if !ok {
		return
	}
	units, installed, err := watchUnitCandidates(ctx, env, bin)
	if err != nil {
		r.fail(fmt.Sprintf("could not read Hand's systemd units (%v); remove any watch unit for a stale home by hand (docs/autostart.md)", err))
		return
	}
	if len(units) == 0 {
		return
	}
	withTransient, err := transientWatchHomes(ctx, env, bin, fleets, installed)
	if err != nil {
		r.fail(fmt.Sprintf("could not list Hand's watcher units (%v); remove any watch unit for a stale home by hand (docs/autostart.md)", err))
		return
	}
	registered := registeredHomes(fleets)
	removed := false
	for _, u := range units {
		if !u.handOwned(o.Target) {
			continue
		}
		if _, stale := u.stale(registered, withTransient); !stale {
			continue
		}
		if r.retireWatchUnit(ctx, o, env, bin, u) {
			removed = true
		}
	}
	if o.Check || !removed {
		return
	}
	if _, err := systemctlOutput(ctx, env, bin, "daemon-reload"); err != nil {
		r.fail(fmt.Sprintf("could not reload systemd (%v); run `systemctl --user daemon-reload`", err))
	}
}

func StaleWatchUnits(ctx context.Context, env []string, target, root string) ([]WatchUnitProblem, error) {
	bin, env, ok := userSystemctl(env)
	if !ok {
		return nil, nil
	}
	units, installed, err := watchUnitCandidates(ctx, env, bin)
	if err != nil || len(units) == 0 {
		return nil, err
	}
	fleets, err := fleet.List(root)
	if err != nil {
		return nil, err
	}
	withTransient, err := transientWatchHomes(ctx, env, bin, fleets, installed)
	if err != nil {
		return nil, err
	}
	registered := registeredHomes(fleets)
	var out []WatchUnitProblem
	for _, u := range units {
		if u.home == "" {
			continue
		}
		missing, stale := u.stale(registered, withTransient)
		if !stale {
			continue
		}
		out = append(out, WatchUnitProblem{Name: u.name, Home: u.home, Missing: missing, Fixable: u.handOwned(target)})
	}
	return out, nil
}

func watchUnitCandidates(ctx context.Context, env []string, bin string) ([]installedWatchUnit, map[string]bool, error) {
	out, err := systemctlOutput(ctx, env, bin, "list-unit-files", "--no-legend", "--plain")
	if err != nil {
		return nil, nil, err
	}
	installed := map[string]bool{}
	var names []string
	for line := range strings.Lines(out) {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "secondhand-watch-") || !strings.HasSuffix(fields[0], ".service") {
			continue
		}
		installed[fields[0]] = true
		names = append(names, fields[0])
	}
	if len(names) == 0 {
		return nil, installed, nil
	}
	out, err = systemctlOutput(ctx, env, bin, append(append([]string{"show"}, names...), "-p", "Id,LoadState,Transient,FragmentPath,ExecStart,Environment,Description")...)
	if err != nil {
		return nil, nil, err
	}
	var units []installedWatchUnit
	for block := range strings.SplitSeq(out, "\n\n") {
		if u, ok := parseWatchUnit(block); ok {
			units = append(units, u)
		}
	}
	return units, installed, nil
}

func registeredHomes(fleets []fleet.Entry) map[string]bool {
	homes := map[string]bool{}
	for _, e := range fleets {
		if e.Home != "" {
			homes[filepath.Clean(e.Home)] = true
		}
	}
	return homes
}

func (u installedWatchUnit) handOwned(target string) bool {
	return strings.HasPrefix(u.description, "Hand watcher for ") && u.command == "watch" && u.home != "" && sameBinary(u.binary, target)
}

func (u installedWatchUnit) stale(registered, transient map[string]bool) (missing, stale bool) {
	home := filepath.Clean(u.home)
	if !filepath.IsAbs(home) {
		return true, true
	}
	if _, err := os.Stat(home); err != nil {
		return true, true
	}
	return false, !registered[home] || transient[home]
}

func (r *Report) retireWatchUnit(ctx context.Context, o Options, env []string, bin string, u installedWatchUnit) bool {
	res := UnitResult{Name: u.name, Action: "remove", Result: "ok"}
	if o.Check {
		res.Result = "would run"
		r.Units = append(r.Units, res)
		return false
	}
	if _, err := systemctlOutput(ctx, env, bin, "disable", "--now", u.name); err != nil {
		res.Result = "failed: " + err.Error()
		r.fail(fmt.Sprintf("could not disable %s (%v); run `systemctl --user disable --now %s`", u.name, err, u.name))
		r.Units = append(r.Units, res)
		return false
	}
	if u.fragment != "" {
		if err := os.Remove(u.fragment); err != nil && !errors.Is(err, fs.ErrNotExist) {
			res.Result = "failed: " + err.Error()
			r.fail(fmt.Sprintf("could not remove %s (%v)", u.fragment, err))
			r.Units = append(r.Units, res)
			return false
		}
	}
	r.Units = append(r.Units, res)
	return true
}

func transientWatchHomes(ctx context.Context, env []string, bin string, fleets []fleet.Entry, installed map[string]bool) (map[string]bool, error) {
	homes := map[string]bool{}
	var want []string
	byUnit := map[string]string{}
	for _, e := range fleets {
		if e.State != "ok" {
			continue
		}
		unit := fleet.WatchUnit(e.ID) + ".service"
		byUnit[unit] = e.Home
		want = append(want, unit)
	}
	if len(want) == 0 {
		return homes, nil
	}
	out, err := systemctlOutput(ctx, env, bin, "list-units", "--all", "--plain", "--no-legend")
	if err != nil {
		return nil, err
	}
	for line := range strings.Lines(out) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		home, ok := byUnit[fields[0]]
		if ok && !installed[fields[0]] {
			homes[filepath.Clean(home)] = true
		}
	}
	return homes, nil
}

func parseWatchUnit(block string) (installedWatchUnit, bool) {
	props := map[string]string{}
	for line := range strings.Lines(block) {
		if k, v, ok := strings.Cut(strings.TrimRight(line, "\n"), "="); ok {
			props[k] = v
		}
	}
	if props["Id"] == "" || props["LoadState"] != "loaded" || props["Transient"] == "yes" {
		return installedWatchUnit{}, false
	}
	path, argv := execStartOf(props["ExecStart"])
	command := ""
	if rest, ok := strings.CutPrefix(argv, path+" "); ok {
		command, _, _ = strings.Cut(rest, " ")
	} else if fields := strings.Fields(argv); len(fields) > 1 {
		command = fields[1]
	}
	return installedWatchUnit{
		name:        props["Id"],
		description: props["Description"],
		home:        envValue(props["Environment"], "HAND_HOME"),
		command:     command,
		binary:      path,
		fragment:    props["FragmentPath"],
	}, true
}

func sameBinary(path, target string) bool {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return path == target
}

func userSystemctl(env []string) (string, []string, bool) {
	run, ok := luvus.UserManager(env)
	if !ok {
		return "", nil, false
	}
	bin := filepath.Join(filepath.Dir(run), "systemctl")
	if fi, err := os.Stat(bin); err != nil || !fi.Mode().IsRegular() {
		return "", nil, false
	}
	return bin, env, true
}

func systemctlOutput(ctx context.Context, env []string, bin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append([]string{"--user"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Env, cmd.Stdout, cmd.Stderr, cmd.WaitDelay = env, &stdout, &stderr, time.Second
	proc.NewGroup(cmd)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("systemctl --user %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()+stdout.String()))
	}
	return stdout.String(), nil
}

func execStartOf(v string) (path, argv string) {
	v = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(v), "{ "), " }")
	for part := range strings.SplitSeq(v, " ; ") {
		if p, ok := strings.CutPrefix(part, "path="); ok {
			path = p
		} else if a, ok := strings.CutPrefix(part, "argv[]="); ok {
			argv = a
		}
	}
	return path, argv
}

func envValue(env, name string) string {
	var assignments []string
	var cur strings.Builder
	quoted := false
	for _, r := range env {
		switch {
		case r == '"':
			quoted = !quoted
		case r == ' ' && !quoted:
			assignments = append(assignments, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	assignments = append(assignments, cur.String())
	for _, a := range assignments {
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v
		}
	}
	return ""
}
