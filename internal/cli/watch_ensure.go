package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/flock"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/proc"
)

var watchExecutable = os.Executable

func (r *runner) watchHeld() (bool, error) {
	f, err := os.OpenFile(filepath.Join(r.home, "watch.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, err
	}
	defer f.Close()
	ok, err := flock.Lock(f, false)
	return !ok && err == nil, err
}

func (r *runner) ensureWatcher(ctx context.Context) (bool, error) {
	if held, err := r.watchHeld(); held || err != nil {
		return false, err
	}
	exe, err := watchExecutable()
	if err != nil {
		return false, err
	}
	env := luvus.Scrub(r.env.Environ())
	if run, ok := luvus.UserManager(env); ok {
		cmd := exec.CommandContext(ctx, run, "--user", "--unit="+fleet.WatchUnit(r.fleet.ID), "--collect", "--setenv=HAND_HOME="+r.home, "--setenv=PATH="+r.env.Getenv("PATH"), exe, "watch")
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			return false, fmt.Errorf("systemd-run: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return true, nil
	}
	log, err := os.OpenFile(filepath.Join(r.home, "watch.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return false, err
	}
	defer log.Close()
	cmd := exec.Command(exe, "watch")
	cmd.Env = append(env, "HAND_HOME="+r.home)
	cmd.Stdout, cmd.Stderr = log, log
	proc.Detach(cmd)
	if err := cmd.Start(); err != nil {
		return false, err
	}
	return true, cmd.Process.Release()
}
