//go:build !linux

package execguard

import (
	"errors"
	"fmt"
	"runtime"
)

func Run(string) (int, error) {
	return 0, fmt.Errorf("exec guard cannot prove process incarnation or descendant cessation on %s: %w", runtime.GOOS, errors.ErrUnsupported)
}
