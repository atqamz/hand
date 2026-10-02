//go:build unix

package cli

import "os"

var HostGetenv = os.Getenv
