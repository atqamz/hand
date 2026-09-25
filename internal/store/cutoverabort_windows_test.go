package store

import (
	"errors"
	"os"
	"testing"
)

// #348 revision 4 "Abort": a held bridge handle stops the restore, keeps the bridge active, and abort retries.
func TestAbortLegacyV18CutoverFreezeRetriesAfterHeldBridge(t *testing.T) {
	home, bridge, _, _, _, _ := canonicalV19CutoverRecoveryFixture(t)
	held, err := os.Open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := abortLegacyV18CutoverFreeze(home); !errors.Is(err, errLegacyV18CutoverReplaceBusy) {
		_ = held.Close()
		t.Fatalf("abort over a held bridge = %v, want busy", err)
	}
	if got, err := legacyV18CutoverFileSHA256(Path(home)); err != nil || got != bridge.BridgeSHA256 {
		_ = held.Close()
		t.Fatalf("active after busy abort = %s, %v; want the bridge", got, err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	aborted, err := abortLegacyV18CutoverFreeze(home)
	if err != nil || aborted.Disposition != legacyV18CutoverAbortedDisposition {
		t.Fatalf("retried abort = %#v, %v", aborted, err)
	}
	if got, err := legacyV18CutoverFileSHA256(Path(home)); err != nil || got != bridge.SourceSHA256 {
		t.Fatalf("retried abort digest = %s, %v; want %s", got, err, bridge.SourceSHA256)
	}
}
