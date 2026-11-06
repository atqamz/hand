//go:build unix

package fsx

import (
	"fmt"
	"os"
	"syscall"
)

func Device(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("%s has no device", path)
	}
	return uint64(st.Dev), nil
}
