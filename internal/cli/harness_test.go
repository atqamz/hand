package cli_test

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fakebin"
)

var handRepo, _ = filepath.Abs("/home/me/hand")

func exe(dir, name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name)
}

func fakeBehaviors() map[string]func([]string) int {
	return map[string]func([]string) int{"fake": fakeMain, "wrap": wrapMain, "reporter": reporterMain, "opener": func(args []string) int {
		fakebin.Append(os.Getenv("XDG_LOG"), args[len(args)-1])
		return 0
	}}
}

func wrapMain(args []string) int {
	p := fakebin.Params()
	fmt.Fprint(os.Stderr, p["stderr"])
	cmd := exec.Command(p["exec"], args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	if err != nil {
		return 1
	}
	return 0
}

func fakeMain(args []string) int {
	p := fakebin.Params()
	if len(args) > 0 {
		if out, ok := p["on "+args[0]]; ok {
			fmt.Print(out)
			return 0
		}
	}
	d, _ := time.ParseDuration(p["sleep"])
	time.Sleep(d)
	if log := cmp.Or(p["log"], os.Getenv(p["logenv"])); log != "" {
		fakebin.Append(log, strings.TrimSpace(p["prefix"]+" "+strings.Join(args, " ")))
	}
	fmt.Fprint(os.Stderr, p["stderr"])
	code, _ := strconv.Atoi(p["exit"])
	return code
}

type harness struct {
	name string
	t    *testing.T
	home string
	mu   *sync.Mutex
	now  time.Time
	vars map[string]string
	cwd  string
	in   io.Reader
	tty  bool
	exec [][]string
}

func newHarness(t *testing.T) *harness {
	return &harness{t: t, mu: new(sync.Mutex), home: t.TempDir(), now: time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC), vars: map[string]string{"SECONDHAND_HOME": t.TempDir()}}
}

func (h *harness) clock() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.now = h.now.Add(d)
}

func (h *harness) env(out, errOut *bytes.Buffer) cli.Env {
	return cli.Env{
		Name:   h.name,
		Stdin:  h.stdin(),
		Stdout: out,
		Stderr: errOut,
		Getenv: func(k string) string {
			if v, ok := h.vars[k]; ok || k != "HAND_HOME" {
				return v
			}
			return h.home
		},
		Environ: func() []string {
			var kv []string
			for k, v := range h.vars {
				kv = append(kv, k+"="+v)
			}
			return kv
		},
		Now: h.clock,
		Exec: func(argv0 string, argv, _ []string) error {
			h.exec = append(h.exec, append([]string{argv0}, argv...))
			return nil
		},
		Terminal: func() bool { return h.tty },
		Getwd: func() (string, error) {
			if h.cwd != "" {
				return h.cwd, nil
			}
			return h.home, nil
		},
	}
}

func (h *harness) stdin() io.Reader {
	if h.in == nil {
		return strings.NewReader("")
	}
	return h.in
}

func (h *harness) run(args ...string) (string, string, int) {
	var out, errOut bytes.Buffer
	code := cli.Run(args, h.env(&out, &errOut))
	return out.String(), errOut.String(), code
}

func (h *harness) ok(args ...string) string {
	h.t.Helper()
	out, errOut, code := h.run(args...)
	if code != 0 {
		h.t.Fatalf("hand %q: exit %d: %s", args, code, errOut)
	}
	return out
}

func (h *harness) runCtx(ctx context.Context, args ...string) (string, string, int) {
	var out, errOut bytes.Buffer
	env := h.env(&out, &errOut)
	env.Context = ctx
	code := cli.Run(args, env)
	return out.String(), errOut.String(), code
}

func field(out, name string) string {
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(l, name+": "); ok {
			if u, err := strconv.Unquote(v); err == nil {
				return u
			}
			return v
		}
	}
	return ""
}

func (h *harness) worktree(name string) string {
	h.t.Helper()
	return filepath.Join(h.vars["SECONDHAND_HOME"], "worktrees", field(h.ok("init", h.home), "id"), name)
}

func (h *harness) branch(name string) string {
	h.t.Helper()
	return "hand/" + filepath.Base(filepath.Dir(h.worktree(name))) + "/" + name
}
