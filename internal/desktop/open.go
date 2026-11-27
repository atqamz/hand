package desktop

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func (a *App) openBoardPage(dest string) bool {
	if args, ok := boardOpenArgs(a.Fleet.ID, dest); ok && args == nil {
		a.Show(BoardScreen)
		return true
	}
	ref, ok := boardViewerRef(a.Fleet.ID, dest)
	if !ok {
		return false
	}
	a.OpenRef(ref)
	return true
}

func (a *App) openBoardLink(dest string) {
	if !a.openBoardPage(dest) {
		a.OpenErr = "this Board page has no page in this window"
	}
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

func boardViewerRef(fleet, dest string) (string, bool) {
	if args, ok := boardOpenArgs(fleet, dest); ok && len(args) == 1 {
		if refDigits(args[0]) {
			return args[0], true
		}
		return "", false
	}
	return refPath(fleet, dest)
}

func refPath(fleet, dest string) (string, bool) {
	u, err := url.Parse(dest)
	if err != nil || fleet == "" || u.Scheme != "" || u.Host != "" {
		return "", false
	}
	rest, ok := strings.CutPrefix(u.Path, "/"+fleet+"/ref/")
	if !ok || !refDigits(rest) {
		return "", false
	}
	return rest, true
}

func refDigits(ref string) bool {
	if len(ref) < 2 || !strings.ContainsRune("tdar", rune(ref[0])) {
		return false
	}
	for _, r := range ref[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

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
	if kind == "doc" && ok && strings.Trim(ref, "/") != "" && !strings.HasPrefix(ref, "/") && !strings.ContainsAny(ref, "\\\x00") {
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
