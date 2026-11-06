package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/state"
)

var scratchEnvVars = []string{"TMPDIR", "TMP", "TEMP", "GOTMPDIR", "CLAUDE_CODE_TMPDIR"}

const (
	unixSocketPathLimit = 104
	unixSocketNameRoom  = 8
)

type exitStatus struct{ code int }

func (e exitStatus) Error() string { return "exit status " + strconv.Itoa(e.code) }

func cmdAttemptExec(r *runner, args []string) error {
	fs := flags("attempt _exec")
	dir := fs.String("dir", "", "")
	root := fs.String("root", "", "")
	if err := fs.Parse(args); err != nil {
		return usageError{fmt.Sprintf("attempt _exec: %v", err)}
	}
	argv := fs.Args()
	scratch, err := attemptScratch(*root, *dir, argv)
	if err != nil {
		return err
	}
	if warn := scratchSocketWarning(scratch); warn != "" {
		fmt.Fprintln(r.env.Stderr, "warning: "+warn)
	}
	if err := makePrivate(scratch); err != nil {
		return err
	}
	err = replaceProcess(argv, scratchEnvironment(r.environ(), scratch))
	if code, ok := processExitCode(err); ok {
		return exitStatus{code}
	}
	return err
}

func scratchShapeUsage(dir string) error {
	return usageError{fmt.Sprintf("attempt _exec: --dir must be the attempt's scratch folder, <fleet home>/scratch/tN-aM, not %s", dir)}
}

func attemptScratch(root, dir string, argv []string) (string, error) {
	if dir == "" || !filepath.IsAbs(dir) || len(argv) == 0 {
		return "", scratchShapeUsage(dir)
	}
	path := filepath.Clean(dir)
	key := filepath.Base(path)
	if _, _, ok := parseScratchKey(key); !ok {
		return "", scratchShapeUsage(dir)
	}
	home := filepath.Dir(filepath.Dir(path))
	if info, err := os.Stat(home); err != nil {
		return "", fmt.Errorf("attempt _exec: the fleet home for %s is gone: %w", path, err)
	} else if !info.IsDir() {
		return "", scratchShapeUsage(dir)
	}
	if info, err := os.Stat(filepath.Join(home, "hand.db")); err != nil || !info.Mode().IsRegular() {
		return "", scratchShapeUsage(dir)
	}
	realHome, err := registeredHome(root, home, key, dir)
	if err != nil {
		return "", err
	}
	for _, p := range []string{filepath.Join(home, "scratch"), path} {
		info, err := os.Lstat(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", scratchShapeUsage(dir)
		}
		if err == nil && !info.IsDir() {
			return "", scratchShapeUsage(dir)
		}
	}
	return filepath.Join(realHome, "scratch", key), nil
}

func sharedRoot(given string) (string, error) {
	if given == "" || !filepath.IsAbs(given) {
		return "", fmt.Errorf("%w: --root must be the shared folder's absolute path", state.ErrInvalid)
	}
	return canonical(given)
}

func registeredHome(given, home, key, dir string) (string, error) {
	realHome, err := canonical(home)
	if err != nil {
		return "", err
	}
	root, err := sharedRoot(given)
	if err != nil {
		return "", fmt.Errorf("attempt _exec: %w", err)
	}
	links, err := os.ReadDir(filepath.Join(root, "fleets"))
	if err != nil {
		return "", scratchShapeUsage(dir)
	}
	claimed := 0
	for _, entry := range links {
		id := entry.Name()
		if !state.FleetID.MatchString(id) {
			continue
		}
		recorded, err := fleet.Home(root, id)
		if err != nil {
			continue
		}
		real, err := canonical(recorded)
		if err != nil || !samePath(real, realHome) {
			continue
		}
		if info, err := os.Lstat(filepath.Join(root, "worktrees", id, key)); err != nil || !info.IsDir() {
			continue
		}
		claimed++
	}
	if claimed != 1 {
		return "", scratchShapeUsage(dir)
	}
	return realHome, nil
}

func scratchSocketWarning(dir string) string {
	if runtime.GOOS == "windows" || len(dir)+unixSocketNameRoom <= unixSocketPathLimit {
		return ""
	}
	return fmt.Sprintf("the scratch folder path is %d bytes; a Unix socket inside it may fail with \"AF_UNIX path too long\" (sun_path holds 104 bytes on macOS and 108 on Linux, including the socket's own name)", len(dir))
}

func (r *runner) environ() []string {
	if r.env.Environ != nil {
		return r.env.Environ()
	}
	return os.Environ()
}

func scratchEnvironment(environ []string, dir string) []string {
	out := make([]string, 0, len(environ)+len(scratchEnvVars))
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(scratchEnvVars, name) {
			out = append(out, kv)
		}
	}
	for _, name := range scratchEnvVars {
		out = append(out, name+"="+dir)
	}
	return out
}

func processExitCode(err error) (int, bool) {
	var exited *exec.ExitError
	if errors.As(err, &exited) {
		return exited.ExitCode(), true
	}
	return 0, false
}
