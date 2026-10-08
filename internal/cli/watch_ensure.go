package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/flock"
)

var (
	watchExecutable  = os.Executable
	watcherStartWait = 3 * time.Second
	systemdRunWait   = 10 * time.Second
)

func (r *runner) watchHeld() (bool, error) {
	f, err := os.OpenFile(filepath.Join(r.home, "watch.lock"), os.O_RDWR, 0)
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

func (r *runner) ensureWatcher(ctx context.Context) (bool, error) {
	if held, err := r.watchHeld(); held || err != nil {
		return false, err
	}
	return r.startService(ctx, spawnSpec{
		unit:    fleet.WatchUnit(r.fleet.ID),
		log:     filepath.Join(r.home, "watch.log"),
		args:    []string{"watch"},
		env:     []string{"HAND_HOME=" + r.home},
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
