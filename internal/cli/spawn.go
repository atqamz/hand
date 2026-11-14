package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/proc"
)

type spawnSpec struct {
	unit    string
	log     string
	dir     string
	args    []string
	props   []string
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
	path := r.env.Getenv("PATH")
	for _, kv := range s.env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	if run, ok := luvus.UserManager(env); ok {
		args := []string{"--user", "--unit=" + s.unit, "--collect", "-p", "Restart=on-failure", "-p", "RestartSec=5", "-p", "RestartPreventExitStatus=3"}
		for _, p := range s.props {
			args = append(args, "-p", p)
		}
		for _, kv := range s.env {
			if strings.HasPrefix(kv, "PATH=") {
				continue
			}
			args = append(args, "--setenv="+kv)
		}
		args = append(args, "--setenv=PATH="+path)
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
	cmd.Env = withEnv(env, s.env...)
	cmd.Stdout, cmd.Stderr = log, log
	proc.Detach(cmd)
	from, _ := log.Seek(0, io.SeekEnd)
	if err := cmd.Start(); err != nil {
		return false, err
	}
	awaitCtx, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	go func() {
		_ = cmd.Wait()
		stop(fmt.Errorf("exited before it answered (%s)%s", cmd.ProcessState, lastLine(s.log, from)))
	}()
	err = s.await(awaitCtx, "read "+s.log)
	if cause := context.Cause(awaitCtx); err != nil && ctx.Err() == nil && cause != nil {
		if up, uerr := s.running(); uerr == nil && up {
			return true, nil
		}
		return true, fmt.Errorf("%w; read %s", cause, s.log)
	}
	return true, err
}

func lastLine(path string, from int64) string {
	b, err := os.ReadFile(path)
	if err != nil || int64(len(b)) < from {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(b[from:])), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return ": " + last
	}
	return ""
}

func withEnv(env []string, kv ...string) []string {
	out := append([]string(nil), env...)
	for _, pair := range kv {
		name, _, _ := strings.Cut(pair, "=")
		kept := out[:0]
		for _, e := range out {
			if n, _, _ := strings.Cut(e, "="); n != name {
				kept = append(kept, e)
			}
		}
		out = append(kept, pair)
	}
	return out
}
