//go:build !linux

package update

import (
	"context"
	"testing"
)

func TestStaleWatchUnitsIsANoOpOffLinux(t *testing.T) {
	problems, err := StaleWatchUnits(context.Background(), nil, "/bin/hand", t.TempDir())
	if problems != nil || err != nil {
		t.Fatalf("StaleWatchUnits = %v, %v", problems, err)
	}
}
