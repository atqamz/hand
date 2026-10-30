//go:build !linux

package sandbox

import (
	"fmt"
	"runtime"
)

type Support struct {
	Backend     string
	ABI         int
	UnixSockets bool
	Reason      string
}

func Probe() Support {
	return Support{Backend: "none", Reason: fmt.Sprintf("boxed is available on Linux only (%s is not supported)", runtime.GOOS)}
}

func ABIVersion() int { return 0 }

func Apply(p Policy) error {
	if p.Mode == Open {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrUnsupported, Probe().Reason)
}
