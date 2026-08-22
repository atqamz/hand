//go:build !test

package toolchain

import "runtime"

func targetPlatform() (string, string) {
	return runtime.GOOS, runtime.GOARCH
}
