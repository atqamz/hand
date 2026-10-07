package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/flock"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/proc"
)

var (
	watchExecutable  = os.Executable
	watcherStartWait = 3 * time.Second
	systemdRunWait   = 10 * time.Second
)

func (r *runner) watchHeld() (bool, error) {
	f, err := os.OpenFile(filepath.Join(r.home, "watch.lock"), os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer flock.Release(f)
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
		args := []string{"--user", "--unit=" + fleet.WatchUnit(r.fleet.ID), "--collect", "-p", "Restart=on-failure", "-p", "RestartSec=5", "--setenv=HAND_HOME=" + r.home, "--setenv=PATH=" + r.env.Getenv("PATH")}
		for _, name := range []string{"SECONDHAND_HOME", "LUVUS_HOME", "HAND_LUVUS_SOCKET", "HOME"} {
			if v := r.env.Getenv(name); v != "" {
				args = append(args, "--setenv="+name+"="+v)
			}
		}
		runCtx, cancel := context.WithTimeout(ctx, systemdRunWait)
		defer cancel()
		cmd := exec.CommandContext(runCtx, run, append(args, exe, "watch")...)
		cmd.Env, cmd.WaitDelay = env, time.Second
		out, err := cmd.CombinedOutput()
		if err == nil {
			return true, r.awaitWatcher(ctx, "run `journalctl --user -u "+fleet.WatchUnit(r.fleet.ID)+"`")
		}
		if f, ferr := os.OpenFile(filepath.Join(r.home, "watch.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); ferr == nil {
			fmt.Fprintf(f, "systemd-run: %v: %s\n", err, bytes.TrimSpace(out))
			f.Close()
		}
		if held, err := r.watchHeld(); held || err != nil {
			return false, err
		}
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
	if err := cmd.Process.Release(); err != nil {
		return false, err
	}
	return true, r.awaitWatcher(ctx, "read "+filepath.Join(r.home, "watch.log"))
}

func (r *runner) awaitWatcher(ctx context.Context, look string) error {
	deadline := time.Now().Add(watcherStartWait)
	for {
		if held, err := r.watchHeld(); held || err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the watcher did not take watch.lock within %s; %s", watcherStartWait, look)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
