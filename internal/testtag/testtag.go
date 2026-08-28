// Package testtag reports whether the test build tag installed the external-tool fakes, and
// refuses a suite that needs them without it. Untagged, hand resolves the real private runtime
// and the real gh, so the suite fails on the machine instead of on the code.
package testtag

import (
	"fmt"
	"os"
)

// Refuse names the tag and exits non-zero, because a pile of failures against absent tools says
// nothing about the one thing that was wrong with how the suite was invoked.
func Refuse() {
	fmt.Fprintln(os.Stderr, "this suite needs the test build tag, which installs the external-tool fakes: run `make test`, or `go test -tags=test ./...`")
	os.Exit(1)
}

// Main is the entire TestMain a package needs when it has no other setup of its own.
func Main(run func() int) {
	if !Present {
		Refuse()
	}
	os.Exit(run())
}
