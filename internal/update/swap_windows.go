package update

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func swap(src, target string) error {
	fail := func(err error) error { return fmt.Errorf("update: cannot replace %s: %w", target, err) }
	tmp, err := stage(src, target)
	if err != nil {
		return fail(err)
	}
	old := target + ".old"
	_ = os.Remove(old)
	if _, err := os.Lstat(old); err == nil {
		old = target + ".old-" + rand.Text()
	}
	if err := os.Rename(target, old); err != nil {
		_ = os.Remove(tmp)
		return fail(err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		if rerr := os.Rename(old, target); rerr != nil {
			return fail(fmt.Errorf("%w; rolling back from %s failed: %v", err, old, rerr))
		}
		return fail(err)
	}
	return nil
}

func CleanOld(exe string) {
	entries, _ := os.ReadDir(filepath.Dir(exe))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), filepath.Base(exe)+".old") {
			_ = os.Remove(filepath.Join(filepath.Dir(exe), e.Name()))
		}
	}
}
