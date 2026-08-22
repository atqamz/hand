//go:build test && !e2e

package toolchain

import "runtime"

func targetPlatform() (string, string) {
	return runtime.GOOS, runtime.GOARCH
}
