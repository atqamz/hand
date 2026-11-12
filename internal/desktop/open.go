package desktop

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var openTimeout = 30 * time.Second

func (a *App) OpenInBrowser() {
	switch a.Screen {
	case TaskScreen, AttemptScreen, DecisionScreen, ReportScreen:
		a.runOpen(a.Ref)
	default:
		a.runOpen()
	}
}

func (a *App) OpenBoardPath(dest string) {
	args, ok := boardOpenArgs(a.Fleet.ID, dest)
	if !ok {
		a.OpenErr = "this Board page is not one `hand open` takes; open it from the Board's fleet page"
		return
	}
	a.runOpen(args...)
}

func (a *App) OpenScreen() { a.runOpen("screen") }

func (a *App) OpenPhone() { a.runOpen("phone") }

func (a *App) SearchTasks() {
	a.runOpen("tasks", "--", strings.TrimSpace(a.TaskQuery))
}

func (a *App) runOpen(args ...string) {
	if a.OpenBusy {
		return
	}
	a.OpenErr = ""
	fleet, home := a.Fleet.ID, a.Fleet.Home
	if fleet == "" {
		return
	}
	if home == "" {
		a.OpenErr = "the fleet's folder is not known, so `hand open` cannot run for it"
		return
	}
	bin, err := handBinary()
	if err != nil {
		a.OpenErr = "`hand` was not found next to hand-desktop or on PATH; install it or add it to PATH"
		return
	}
	argv := append([]string{"--home", home, "open"}, args...)
	timeout := openTimeout
	run := func() string {
		ctx, cancel := context.WithTimeout(a.Context, timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, argv...)
		cmd.Dir = home
		cmd.WaitDelay = time.Second
		out, err := cmd.CombinedOutput()
		if err == nil {
			return ""
		}
		if ctx.Err() != nil {
			return "`hand open` timed out"
		}
		return "`hand open` failed: " + failureLine(out, err)
	}
	if a.Post == nil {
		a.OpenErr = run()
		return
	}
	a.OpenBusy = true
	go func() {
		msg := run()
		a.Post(func() {
			if a.Fleet.ID != fleet {
				return
			}
			a.OpenBusy = false
			if msg != "" {
				a.OpenErr = msg
			}
		})
	}()
}

func handBinary() (string, error) {
	name := "hand"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if exe, err := os.Executable(); err == nil {
		next := filepath.Join(filepath.Dir(exe), name)
		if info, err := os.Stat(next); err == nil && info.Mode().IsRegular() {
			return next, nil
		}
	}
	return exec.LookPath("hand")
}

func failureLine(out []byte, err error) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return last
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return fmt.Sprintf("exit status %d", exit.ExitCode())
	}
	return err.Error()
}

var boardKinds = map[string]byte{"task": 't', "decision": 'd', "report": 'r', "attempt": 'a'}

func boardOpenArgs(fleet, dest string) ([]string, bool) {
	u, err := url.Parse(dest)
	if err != nil || fleet == "" {
		return nil, false
	}
	rest, ok := strings.CutPrefix(u.Path, "/"+fleet)
	if !ok || rest != "" && rest[0] != '/' {
		return nil, false
	}
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return nil, true
	}
	kind, ref, ok := strings.Cut(rest, "/")
	if kind == "doc" && ok && strings.Trim(ref, "/") != "" {
		return []string{"--doc", ref}, true
	}
	prefix, known := boardKinds[kind]
	if !ok || !known || len(ref) < 2 || ref[0] != prefix {
		return nil, false
	}
	for _, r := range ref[1:] {
		if r < '0' || r > '9' {
			return nil, false
		}
	}
	return []string{ref}, true
}
