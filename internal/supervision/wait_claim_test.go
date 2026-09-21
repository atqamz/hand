package supervision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/filelock"
	"github.com/atqamz/hand/internal/orientation"
)

func TestWaitSerializesHandoverWithWakeClaim(t *testing.T) {
	home := t.TempDir()
	originalRuntimeLease := acquireWaiterGenerationLease
	originalHandLease := acquireWaiterHandGenerationLease
	acquireWaiterGenerationLease = func(string, string, string, string, string) (func() error, error) {
		return func() error { return nil }, nil
	}
	acquireWaiterHandGenerationLease = acquireWaiterGenerationLease
	t.Cleanup(func() {
		acquireWaiterGenerationLease = originalRuntimeLease
		acquireWaiterHandGenerationLease = originalHandLease
	})
	ledger := OpenLedger(home)
	checked, held := false, false
	// The existing clock hook runs inside the actual ledger write lock.
	// Try the real attachment lock there, without sleeps or new production hooks.
	ledger.now = func() time.Time {
		checked = true
		lock, err := os.OpenFile(attachmentLockPath(home), os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		err = filelock.Lock(lock, false)
		if errors.Is(err, filelock.ErrBusy) {
			held = true
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
			return time.Now()
		}
		if err != nil {
			_ = lock.Close()
			t.Fatal(err)
		}
		if err := errors.Join(filelock.Unlock(lock), lock.Close()); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(attachmentPath(home))
		if err != nil {
			t.Fatal(err)
		}
		var successor AttachmentRecord
		if err := json.Unmarshal(data, &successor); err != nil {
			t.Fatal(err)
		}
		successor.WaiterID += "-successor"
		if acquired, err := AcquireAttachment(home, successor); err != nil || !acquired {
			t.Fatalf("handover positive control = %t, %v", acquired, err)
		}
		return time.Now()
	}
	wake, err := Wait(context.Background(), Waiter{
		Home: home, Ledger: ledger,
		ReadEvidence: fixedReader(orientation.Evidence{FleetID: "f_1", Actionable: []orientation.ActionableEvidence{
			actionableEvidence("task-1", "episode-1", "blocked"),
		}}),
	}, WaitConfig{
		Host: "codex", RuntimeSession: "session-a", RuntimeGeneration: "hand-a",
		LeaseGeneration: "runtime-a", PollInterval: time.Hour,
	})
	if err != nil || len(wake.Episodes) != 1 {
		t.Fatalf("wake = %+v, %v", wake, err)
	}
	if !checked || !held {
		t.Fatal("wake claim admitted attachment handover during ledger commit")
	}
}

func TestClaimLeavesEpisodeForExactSuccessor(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	record := AttachmentRecord{Host: "codex", Runtime: "session-a", Generation: "hand-a", WaiterID: "old", FleetID: "f_1", StartedAt: now, HeartbeatAt: now, ExpiresAt: now.Add(time.Minute)}
	if acquired, err := AcquireAttachment(home, record); err != nil || !acquired {
		t.Fatalf("acquire = %t, %v", acquired, err)
	}
	old := &bridgeGuard{home: home, record: record, lease: time.Minute}
	if err := old.prove(); err != nil {
		t.Fatal(err)
	}
	successor := record
	successor.WaiterID = "new"
	if acquired, err := AcquireAttachment(home, successor); err != nil || !acquired {
		t.Fatalf("handover = %t, %v", acquired, err)
	}
	before, err := os.ReadFile(attachmentPath(home))
	if err != nil {
		t.Fatal(err)
	}
	evidence := orientation.Evidence{FleetID: "f_1", Actionable: []orientation.ActionableEvidence{actionableEvidence("task-1", "episode-1", "blocked")}}
	episodes := FromEvidence(evidence)
	waiter := Waiter{Home: home, Ledger: OpenLedger(home)}
	wake := Wake{Host: "codex", FleetID: "f_1"}
	if result, won, err := claimAndDeliver(old, waiter, wake, episodes); !errors.Is(err, ErrBridgeOwned) || won || len(result.Episodes) != 0 {
		t.Fatalf("stale claim = %+v, %t, %v", result, won, err)
	}
	if got := waiter.Ledger.Eligible(episodes); len(got) != 1 {
		t.Fatalf("stale holder consumed successor episode: %+v", got)
	}
	current := &bridgeGuard{home: home, record: successor, lease: time.Minute}
	if result, won, err := claimAndDeliver(current, waiter, wake, episodes); err != nil || !won || len(result.Episodes) != 1 {
		t.Fatalf("successor claim = %+v, %t, %v", result, won, err)
	}
	after, err := os.ReadFile(attachmentPath(home))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("claim rewrote attachment: %v", err)
	}
}
