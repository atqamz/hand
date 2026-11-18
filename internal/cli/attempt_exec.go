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

const compactEnvVar = "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE"

var envNamesFold = runtime.GOOS == "windows"

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
	compact := fs.Int("compact-percent", 0, "")
	if err := fs.Parse(args); err != nil {
		return usageError{fmt.Sprintf("attempt _exec: %v", err)}
	}
	if *compact < 0 || *compact > 100 {
		return usageError{fmt.Sprintf("attempt _exec: --compact-percent is %d; give 0 to 100, 0 is off", *compact)}
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
	err = replaceProcess(argv, scratchEnvironment(r.environ(), scratch, *compact))
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
	expected := filepath.Join(realHome, "scratch", key)
	realPath, err := canonical(path)
	if err != nil {
		return "", err
	}
	if !samePath(realPath, expected) {
		return "", scratchShapeUsage(dir)
	}
	return expected, nil
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
	ids, err := fleet.IDs(root)
	if err != nil {
		return "", scratchShapeUsage(dir)
	}
	claimed := 0
	for _, id := range ids {
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

func scratchEnvironment(environ []string, dir string, compact int) []string {
	out := make([]string, 0, len(environ)+len(scratchEnvVars)+1)
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if slices.Contains(scratchEnvVars, name) || compact > 0 && sameEnvName(name, compactEnvVar) {
			continue
		}
		out = append(out, kv)
	}
	for _, name := range scratchEnvVars {
		out = append(out, name+"="+dir)
	}
	if compact > 0 {
		out = append(out, compactEnvVar+"="+strconv.Itoa(compact))
	}
	return out
}

func sameEnvName(a, b string) bool {
	if envNamesFold {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func processExitCode(err error) (int, bool) {
	var exited *exec.ExitError
	if errors.As(err, &exited) {
		return exited.ExitCode(), true
	}
	return 0, false
}
