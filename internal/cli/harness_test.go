package cli_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
)

type harness struct {
	name string
	t    *testing.T
	home string
	now  time.Time
	vars map[string]string
	cwd  string
}

func newHarness(t *testing.T) *harness {
	return &harness{t: t, home: t.TempDir(), now: time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC), vars: map[string]string{"SECONDHAND_HOME": t.TempDir()}}
}

func (h *harness) env(out, errOut *bytes.Buffer) cli.Env {
	return cli.Env{
		Name:   h.name,
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
		Now: func() time.Time { return h.now },
		Getwd: func() (string, error) {
			if h.cwd != "" {
				return h.cwd, nil
			}
			return h.home, nil
		},
	}
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
