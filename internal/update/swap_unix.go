//go:build unix

package update

import (
	"fmt"
	"os"
)

func swap(src, target string) error {
	tmp, err := stage(src, target)
	if err == nil {
		if err = os.Rename(tmp, target); err != nil {
			_ = os.Remove(tmp)
		}
	}
	if err != nil {
		return fmt.Errorf("update: cannot replace %s: %w", target, err)
	}
	return nil
}

func CleanOld(string) {}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
