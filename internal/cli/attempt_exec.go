package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

var scratchEnvVars = []string{"TMPDIR", "TMP", "TEMP", "GOTMPDIR", "CLAUDE_CODE_TMPDIR"}

type exitStatus struct{ code int }

func (e exitStatus) Error() string { return "exit status " + strconv.Itoa(e.code) }

func cmdAttemptExec(r *runner, args []string) error {
	fs := flags("attempt _exec")
	dir := fs.String("dir", "", "")
	if err := fs.Parse(args); err != nil {
		return usageError{fmt.Sprintf("attempt _exec: %v", err)}
	}
	argv := fs.Args()
	if *dir == "" || !filepath.IsAbs(*dir) || len(argv) == 0 {
		return usageError{"usage: hand attempt _exec --dir DIR -- COMMAND [ARG...]"}
	}
	if err := makePrivate(*dir); err != nil {
		return err
	}
	err := replaceProcess(argv, scratchEnvironment(r.environ(), *dir))
	if code, ok := processExitCode(err); ok {
		return exitStatus{code}
	}
	return err
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
