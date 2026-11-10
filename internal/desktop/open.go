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
		a.openRef(a.Ref)
	default:
		a.openRef("")
	}
}

func (a *App) OpenBoardPath(dest string) {
	ref, ok := boardRef(a.Fleet.ID, dest)
	if !ok {
		a.OpenErr = "this Board page has no ref that `hand open` takes; open it from the Board's fleet page"
		return
	}
	a.openRef(ref)
}

func (a *App) openRef(ref string) {
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
	argv := []string{"--home", home, "open"}
	if ref != "" {
		argv = append(argv, ref)
	}
	run := func() string {
		ctx, cancel := context.WithTimeout(a.Context, openTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, argv...)
		cmd.Dir = home
		cmd.WaitDelay = time.Second
		out, err := cmd.CombinedOutput()
		if err == nil {
			return ""
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
			a.OpenBusy = false
			if msg != "" && a.Fleet.ID == fleet {
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

func boardRef(fleet, dest string) (string, bool) {
	u, err := url.Parse(dest)
	if err != nil || fleet == "" {
		return "", false
	}
	rest, ok := strings.CutPrefix(u.Path, "/"+fleet)
	if !ok || rest != "" && rest[0] != '/' {
		return "", false
	}
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return "", true
	}
	kind, ref, ok := strings.Cut(rest, "/")
	prefix, known := boardKinds[kind]
	if !ok || !known || len(ref) < 2 || ref[0] != prefix {
		return "", false
	}
	for _, r := range ref[1:] {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return ref, true
}
