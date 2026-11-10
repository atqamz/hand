package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/flock"
	"github.com/atqamz/hand/internal/state"
)

const watchGenerationFile = "watch.generation"

var (
	watchExecutable  = os.Executable
	watcherStartWait = 3 * time.Second
	systemdRunWait   = 10 * time.Second
)

func (r *runner) watchHeld() (bool, error) { return watchLocked(r.home) }

func watchLocked(home string) (bool, error) {
	f, err := os.OpenFile(filepath.Join(home, "watch.lock"), os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer flock.Release(f)
	ok, err := flock.Lock(f, false)
	return !ok && err == nil, err
}

func (r *runner) watcherGeneration() string {
	b, err := os.ReadFile(filepath.Join(r.home, watchGenerationFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (r *runner) writeWatchGeneration() {
	path := filepath.Join(r.home, watchGenerationFile)
	if err := writeFile(r.home, path, r.generation+"\n"); err != nil {
		fmt.Fprintf(r.env.Stderr, "warning: cannot write %s: %v; the board cannot tell which generation this watcher holds\n", path, err)
	}
}

func (r *runner) ensureWatcher(ctx context.Context) (bool, error) {
	r.recordGeneration()
	held, err := r.watchHeld()
	if err != nil {
		return false, err
	}
	if held {
		if r.watcherGeneration() == r.generation {
			return false, nil
		}
		if _, _, err := stopPIDFile("watcher", filepath.Join(r.home, "watch.pid")); err != nil {
			return false, err
		}
		if held, err := r.watchHeld(); err != nil {
			return false, err
		} else if held {
			return false, fmt.Errorf("%w: a watcher holding another board generation still holds watch.lock; stop it with `%s watch stop`", state.ErrConflict, r.env.command())
		}
	}
	env := []string{"HAND_HOME=" + r.home}
	if r.generation != "" {
		env = append(env, "HAND_GENERATION="+r.generation)
	}
	return r.startService(ctx, spawnSpec{
		unit:    fleet.WatchUnit(r.fleet.ID),
		log:     filepath.Join(r.home, "watch.log"),
		args:    []string{"watch"},
		env:     env,
		running: r.watchHeld,
		await:   r.awaitWatcher,
	})
}

func (r *runner) awaitWatcher(ctx context.Context, look string) error {
	deadline := time.Now().Add(watcherStartWait)
	for {
		if held, err := r.watchHeld(); held || err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the watcher did not take watch.lock within %s; %s", watcherStartWait, look)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
