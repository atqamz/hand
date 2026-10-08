package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/proc"
)

type spawnSpec struct {
	unit    string
	log     string
	dir     string
	args    []string
	env     []string
	running func() (bool, error)
	await   func(ctx context.Context, look string) error
}

func (r *runner) startService(ctx context.Context, s spawnSpec) (bool, error) {
	exe, err := watchExecutable()
	if err != nil {
		return false, err
	}
	env := luvus.Scrub(r.env.Environ())
	if run, ok := luvus.UserManager(env); ok {
		args := []string{"--user", "--unit=" + s.unit, "--collect", "-p", "Restart=on-failure", "-p", "RestartSec=5", "-p", "RestartPreventExitStatus=3"}
		for _, kv := range s.env {
			args = append(args, "--setenv="+kv)
		}
		args = append(args, "--setenv=PATH="+r.env.Getenv("PATH"))
		for _, name := range []string{"SECONDHAND_HOME", "LUVUS_HOME", "HAND_LUVUS_SOCKET", "HOME"} {
			if v := r.env.Getenv(name); v != "" {
				args = append(args, "--setenv="+name+"="+v)
			}
		}
		runCtx, cancel := context.WithTimeout(ctx, systemdRunWait)
		defer cancel()
		cmd := exec.CommandContext(runCtx, run, append(args, append([]string{exe}, s.args...)...)...)
		cmd.Env, cmd.WaitDelay = env, time.Second
		out, err := cmd.CombinedOutput()
		if err == nil {
			return true, s.await(ctx, "run `journalctl --user -u "+s.unit+"`")
		}
		if f, ferr := os.OpenFile(s.log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); ferr == nil {
			fmt.Fprintf(f, "systemd-run: %v: %s\n", err, bytes.TrimSpace(out))
			f.Close()
		}
		if up, err := s.running(); up || err != nil {
			return false, err
		}
	}
	log, err := os.OpenFile(s.log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return false, err
	}
	defer log.Close()
	cmd := exec.Command(exe, s.args...)
	cmd.Dir = s.dir
	cmd.Env = append(env, s.env...)
	cmd.Stdout, cmd.Stderr = log, log
	proc.Detach(cmd)
	if err := cmd.Start(); err != nil {
		return false, err
	}
	if err := cmd.Process.Release(); err != nil {
		return false, err
	}
	return true, s.await(ctx, "read "+s.log)
}
