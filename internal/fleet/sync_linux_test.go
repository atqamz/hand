package fleet_test

import (
	"testing"

	"github.com/atqamz/hand/internal/fleet"
)

func TestSyncDirIgnoresAFilesystemThatCannotSyncFolders(t *testing.T) {
	if err := fleet.SyncDir("/proc/self"); err != nil {
		t.Fatalf("syncDir of a folder that cannot be synced = %v", err)
	}
}
