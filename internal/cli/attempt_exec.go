package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

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
	if err := fs.Parse(args); err != nil {
		return usageError{fmt.Sprintf("attempt _exec: %v", err)}
	}
	argv := fs.Args()
	scratch, err := attemptScratch(*dir, argv)
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

func attemptScratch(dir string, argv []string) (string, error) {
	if dir == "" || !filepath.IsAbs(dir) || len(argv) == 0 {
		return "", scratchShapeUsage(dir)
	}
	path := filepath.Clean(dir)
	key := filepath.Base(path)
	taskID, attemptID, ok := parseScratchKey(key)
	if !ok {
		return "", scratchShapeUsage(dir)
	}
	home := filepath.Dir(filepath.Dir(path))
	if info, err := os.Stat(home); err != nil {
		return "", fmt.Errorf("attempt _exec: the fleet home for %s is gone: %w", path, err)
	} else if !info.IsDir() {
		return "", scratchShapeUsage(dir)
	}
	db := filepath.Join(home, "hand.db")
	if info, err := os.Stat(db); err != nil || !info.Mode().IsRegular() {
		return "", scratchShapeUsage(dir)
	}
	st, err := state.Open(db, time.Now)
	if err != nil {
		return "", fmt.Errorf("attempt _exec: %w", err)
	}
	defer st.Close()
	ctx := context.Background()
	a, err := st.Attempt(ctx, attemptID)
	if errors.Is(err, state.ErrNotFound) {
		return "", scratchShapeUsage(dir)
	}
	if err != nil {
		return "", fmt.Errorf("attempt _exec: %w", err)
	}
	if a.TaskID != taskID || filepath.Base(a.Worktree) != key {
		return "", scratchShapeUsage(dir)
	}
	f, err := st.Fleet(ctx)
	if err != nil {
		return "", fmt.Errorf("attempt _exec: %w", err)
	}
	wt := filepath.Clean(a.Worktree)
	root := filepath.Dir(filepath.Dir(filepath.Dir(wt)))
	id := filepath.Base(filepath.Dir(wt))
	if !filepath.IsAbs(wt) || f.ID != id || filepath.Base(filepath.Dir(filepath.Dir(wt))) != "worktrees" {
		return "", scratchShapeUsage(dir)
	}
	recorded, err := fleet.Home(root, id)
	if err != nil {
		return "", fmt.Errorf("attempt _exec: the fleet home for %s is gone: %w", path, err)
	}
	realHome, err := canonical(recorded)
	if err != nil {
		return "", err
	}
	claimant, err := canonical(home)
	if err != nil {
		return "", err
	}
	if !samePath(realHome, claimant) {
		return "", scratchShapeUsage(dir)
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
