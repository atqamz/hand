package boot

import (
	"strings"

	"golang.org/x/sys/unix"
)

func ID() string {
	s, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}
