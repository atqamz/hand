//go:build unix

package fsx

import (
	"fmt"
	"os"
	"syscall"
)

func Links(path string) (uint64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("%s has no link count", path)
	}
	return uint64(st.Nlink), nil
}
