package cli

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/proc"
	"github.com/atqamz/hand/internal/update"
)

var updateMin = 10 * time.Minute

func everyPath(root string) string { return filepath.Join(root, "board.update-every") }

func recordEvery(root string, every time.Duration) error {
	if every == 0 {
		if err := os.Remove(everyPath(root)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	return writeFile(root, everyPath(root), every.String()+"\n")
}

func recordedEvery(root string) time.Duration {
	b, err := os.ReadFile(everyPath(root))
	if err != nil {
		return 0
	}
	d, err := time.ParseDuration(strings.TrimSpace(string(b)))
	if err != nil || d < updateMin {
		return 0
	}
	return d
}

func (r *runner) startUpdate(root string, live *atomic.Bool) error {
	if live.Load() {
		return nil
	}
	if held, err := update.Running(root); held || err != nil {
		return err
	}
	exe, err := watchExecutable()
	if err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(root, "update.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(exe, "update", "--channel", Channel, "--keep-luvus")
	cmd.Dir, cmd.Env = root, luvus.Scrub(r.env.Environ())
	cmd.Stdout, cmd.Stderr = log, log
	proc.Detach(cmd)
	proc.NoWindow(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	live.Store(true)
	go func() {
		_ = cmd.Wait()
		live.Store(false)
	}()
	return nil
}
