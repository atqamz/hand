package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

const boardUnit = "secondhand-board"

var boardStartWait = 10 * time.Second

func (r *runner) ensureBoard(ctx context.Context, token string) error {
	up := func() (bool, error) {
		_, err := boardAddr(ctx, r.root, r.fleet.ID, token)
		if errors.Is(err, state.ErrNotFound) {
			return false, nil
		}
		return err == nil, err
	}
	if ok, err := up(); ok || err != nil {
		return err
	}
	unlock, err := lockPath(filepath.Join(r.root, "board.start.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	if ok, err := up(); ok || err != nil {
		return err
	}
	if err := r.startBoard(ctx, up); err != nil {
		return fmt.Errorf("%w: no board is running and starting one failed: %v; %s", state.ErrNotFound, err, boardHint)
	}
	return nil
}

func (r *runner) startBoard(ctx context.Context, up func() (bool, error)) error {
	log := filepath.Join(r.root, "board.log")
	await := func(ctx context.Context, look string) error {
		deadline := time.Now().Add(boardStartWait)
		for {
			if ok, err := up(); ok || err != nil {
				return err
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("the board did not answer within %s; %s", boardStartWait, look)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	env := luvus.Scrub(r.env.Environ())
	if run, ok := luvus.UserManager(env); ok {
		systemctl := filepath.Join(filepath.Dir(run), "systemctl")
		runCtx, cancel := context.WithTimeout(ctx, systemdRunWait)
		defer cancel()
		show := exec.CommandContext(runCtx, systemctl, "--user", "show", "-p", "LoadState", "--value", boardUnit+".service")
		show.Env, show.WaitDelay = env, time.Second
		if out, err := show.Output(); err == nil && strings.TrimSpace(string(out)) == "loaded" {
			start := exec.CommandContext(runCtx, systemctl, "--user", "start", boardUnit+".service")
			start.Env, start.WaitDelay = env, time.Second
			if out, err := start.CombinedOutput(); err != nil {
				return fmt.Errorf("systemctl --user start %s.service: %w: %s", boardUnit, err, bytes.TrimSpace(out))
			}
			return await(ctx, "run `journalctl --user -u "+boardUnit+"`")
		}
	}
	_, err := r.startService(ctx, spawnSpec{
		unit:    boardUnit,
		log:     log,
		dir:     r.root,
		args:    []string{"board"},
		running: up,
		await:   await,
	})
	return err
}
