package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
)

type harness struct {
	t    *testing.T
	home string
	now  time.Time
	vars map[string]string
}

func newHarness(t *testing.T) *harness {
	return &harness{t: t, home: t.TempDir(), now: time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC), vars: map[string]string{}}
}

func (h *harness) env(out, errOut *bytes.Buffer) cli.Env {
	return cli.Env{
		Stdout: out,
		Stderr: errOut,
		Getenv: func(k string) string {
			if k == "HAND_HOME" {
				return h.home
			}
			return h.vars[k]
		},
		Environ: func() []string {
			var kv []string
			for k, v := range h.vars {
				kv = append(kv, k+"="+v)
			}
			return kv
		},
		Now: func() time.Time { return h.now },
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
