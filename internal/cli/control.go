package cli

import (
	"bytes"
	"context"
	"io"
	"strings"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

type controlError struct {
	msg  string
	kind error
}

func (e controlError) Error() string { return e.msg }

func (e controlError) Unwrap() error { return e.kind }

func control(env Env, home string) func(context.Context, ...string) error {
	return func(ctx context.Context, args ...string) error {
		var stderr bytes.Buffer
		run := env
		run.Stdout, run.Stderr, run.Context = io.Discard, &stderr, context.WithoutCancel(ctx)
		code := Run(append([]string{"--home", home}, args...), run)
		if code == 0 {
			return nil
		}
		msg := board.Scrub(strings.TrimPrefix(strings.TrimSpace(stderr.String()), "error: "))
		msg = strings.ReplaceAll(msg, " not found on PATH", " not found on PATH (searched the board's PATH, the one it started with; restart the board after you change it)")
		switch code {
		case 2:
			return controlError{msg, state.ErrInvalid}
		case 3:
			return controlError{msg, state.ErrConflict}
		}
		return controlError{msg: msg}
	}
}
