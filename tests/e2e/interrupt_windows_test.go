//go:build e2e && windows

package e2e

import (
	"testing"
	"time"
)

func (b *backgroundHand) interrupt(t *testing.T, timeout time.Duration) invocation {
	t.Helper()
	b.process.stop()
	return b.waitForExit(t, timeout, "portable Windows cleanup")
}
