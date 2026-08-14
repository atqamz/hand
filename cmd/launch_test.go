package cmd

import (
	"testing"
	"time"

	"github.com/atqamz/hand/internal/runtime"
)

func useFastLaunchPolling(t *testing.T) {
	t.Helper()
	restore := runtime.ConfigureLaunchPollingForTest(time.Millisecond, 10*time.Second, time.Millisecond, 3, 60)
	t.Cleanup(restore)
}

func expectLaunchTimeout() { runtime.SetLaunchTimeoutForTest(150 * time.Millisecond) }
