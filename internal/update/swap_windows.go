package update

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
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
		_ = os.Rename(old, target)
		_ = os.Remove(tmp)
		return fail(err)
	}
	return nil
}

func CleanOld(exe string) {
	olds, _ := filepath.Glob(exe + ".old*")
	for _, p := range olds {
		_ = os.Remove(p)
	}
}
