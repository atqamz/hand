package update

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/atqamz/hand/internal/fsx"
)

func swap(src, target string) error {
	fail := func(err error) error { return fmt.Errorf("update: cannot replace %s: %w", target, err) }
	tmp, err := stage(src, target)
	if err != nil {
		return fail(err)
	}
	if _, err := os.Lstat(target); errors.Is(err, fs.ErrNotExist) {
		if err := fsx.Rename(tmp, target); err != nil {
			_ = os.Remove(tmp)
			return fail(err)
		}
		return nil
	}
	old := target + ".old"
	_ = os.Remove(old)
	if _, err := os.Lstat(old); err == nil {
		old = target + ".old-" + rand.Text()
	}
	if err := fsx.Rename(target, old); err != nil {
		_ = os.Remove(tmp)
		return fail(err)
	}
	if err := fsx.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		if rerr := fsx.Rename(old, target); rerr != nil {
			return fail(fmt.Errorf("%w; rolling back from %s failed: %v", err, old, rerr))
		}
		return fail(err)
	}
	return nil
}

func CleanOld(exe string) { cleanOld(exe) }

func syncDir(string) error { return nil }
