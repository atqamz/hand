package cli

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/atqamz/hand/internal/state"
)

var absPath = regexp.MustCompile(`(^|[\s"'(=])/[^\s"'():,]+`)

type controlError struct {
	msg  string
	kind error
}

func (e controlError) Error() string { return e.msg }

func (e controlError) Unwrap() error { return e.kind }

func supervisorControl(env Env, home string) func(context.Context, ...string) error {
	return func(ctx context.Context, args ...string) error {
		var stderr bytes.Buffer
		run := env
		run.Stdout, run.Stderr, run.Context = io.Discard, &stderr, ctx
		code := Run(append([]string{"--home", home, "supervisor"}, args...), run)
		if code == 0 {
			return nil
		}
		msg := absPath.ReplaceAllStringFunc(strings.TrimPrefix(strings.TrimSpace(stderr.String()), "error: "), func(m string) string {
			i := strings.IndexByte(m, '/')
			return m[:i] + filepath.Base(m[i:])
		})
		switch code {
		case 2:
			return controlError{msg, state.ErrInvalid}
		case 3:
			return controlError{msg, state.ErrConflict}
		}
		return controlError{msg: msg}
	}
}
