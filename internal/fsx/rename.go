package fsx

import (
	"os"
	"time"
)

func Rename(oldpath, newpath string) error {
	deadline := time.Now().Add(2 * time.Second)
	for delay := time.Millisecond; ; delay *= 2 {
		err := os.Rename(oldpath, newpath)
		if err == nil || !held(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(min(delay, 100*time.Millisecond))
	}
}
