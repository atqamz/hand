//go:build test && e2e

package toolchain

import "runtime"

func targetPlatform() (string, string) {
	if runtime.GOOS == "darwin" {
		return "linux", "amd64"
	}
	return runtime.GOOS, runtime.GOARCH
}
