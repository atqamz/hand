package supervision

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testLedger(t *testing.T) (*Ledger, *time.Time) {
	t.Helper()
	home := t.TempDir()
	ledger := OpenLedger(home)
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	ledger.now = func() time.Time { return now }
	t.Cleanup(func() { ledger.now = time.Now })
	return ledger, &now
}

func episode(id, generation string) Episode {
	return Episode{
		FleetID: "f_1", TargetID: id, TargetKind: "task",
		Currentness: currentnessOf("f_1", generation), Kind: "blocked",
	}
}

func TestUnseenEpisodeIsEligibleOnceThenDeduped(t *testing.T) {
	ledger, _ := testLedger(t)
	e := episode("t1", "g1")

	if eligible := ledger.Eligible([]Episode{e}); len(eligible) != 1 {
		t.Fatalf("eligible = %d, want the unseen episode eligible", len(eligible))
	}
	if err := ledger.MarkRequested([]Episode{e}); err != nil {
		t.Fatal(err)
	}
	if eligible := ledger.Eligible([]Episode{e}); len(eligible) != 0 {
		t.Fatal("a requested wake must not spam parallel attempts")
	}
}

func TestRequestedWakeRetriesAfterDeliveryWindow(t *testing.T) {
	ledger, now := testLedger(t)
	e := episode("t1", "g1")
	if err := ledger.MarkRequested([]Episode{e}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(DefaultPolicy().DeliveryTimeout)
	if eligible := ledger.Eligible([]Episode{e}); len(eligible) != 1 {
		t.Fatal("a delivery that never got accepted becomes retry-eligible after the bounded window")
	}
}

func TestAcceptedWakeAwaitsOrientProgressBeforeRetry(t *testing.T) {
	ledger, now := testLedger(t)
	e := episode("t1", "g1")
	if err := ledger.MarkRequested([]Episode{e}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.MarkAccepted(Keys([]Episode{e})); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(DefaultPolicy().DeliveryTimeout)
	if eligible := ledger.Eligible([]Episode{e}); len(eligible) != 0 {
		t.Fatal("accepted delivery inside the progress window is not a failure")
	}
	*now = now.Add(DefaultPolicy().ProgressTimeout + DefaultPolicy().DeliveryTimeout)
	if eligible := ledger.Eligible([]Episode{e}); len(eligible) != 1 {
		t.Fatal("acceptance without any orient progress becomes retry-eligible after the bounded window")
	}
}

func TestOrientedEpisodeNeverRewakesUntilCurrentnessChanges(t *testing.T) {
	ledger, now := testLedger(t)
	e := episode("t1", "g1")
	if err := ledger.MarkRequested([]Episode{e}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.MarkOriented([]Episode{e}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(MaxLedgerAge / 2)
	if eligible := ledger.Eligible([]Episode{e}); len(eligible) != 0 {
		t.Fatal("an unchanged oriented condition must not spin immediate infinite turns")
	}
	next := episode("t1", "g2")
	if eligible := ledger.Eligible([]Episode{next}); len(eligible) != 1 {
		t.Fatal("new evidence/currentness is a new episode and is eligible normally")
	}

	// Past the mechanism-memory bound the exact stale episode becomes
	// eligible once more: the explicit bounded recovery policy, so a
	// condition that outlives the ledger cannot be silenced forever.
	*now = now.Add(MaxLedgerAge + time.Minute)
	if eligible := ledger.Eligible([]Episode{e}); len(eligible) != 1 {
		t.Fatal("aged mechanism memory loses its dedupe and may wake again")
	}
}

func TestLedgerSurvivesRestartAndRecoversFromCorruption(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ledgerRel)
	ledger := OpenLedger(home)
	e := episode("t1", "g1")
	if err := ledger.MarkOriented([]Episode{e}); err != nil {
		t.Fatal(err)
	}
	reopened := OpenLedger(home)
	if eligible := reopened.Eligible([]Episode{e}); len(eligible) != 0 {
		t.Fatal("mechanism memory must survive a process restart")
	}

	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	corrupted := OpenLedger(home)
	// Corruption costs at most one bounded duplicate wake; it never errors.
	if eligible := corrupted.Eligible([]Episode{e}); len(eligible) != 1 {
		t.Fatal("corrupted ledger recovers as disposable empty state")
	}
	if err := corrupted.MarkRequested([]Episode{e}); err != nil {
		t.Fatalf("write after corruption: %v", err)
	}
}

func TestLedgerPrunesAgeAndCount(t *testing.T) {
	ledger, now := testLedger(t)
	for i := range MaxLedgerEntries + 10 {
		e := episode(string(rune('a'+i%26))+string(rune('a'+i/26)), "g")
		if err := ledger.MarkRequested([]Episode{e}); err != nil {
			t.Fatal(err)
		}
		*now = now.Add(time.Second)
	}
	file := ledger.read()
	if len(file.Episodes) > MaxLedgerEntries {
		t.Fatalf("entries = %d, want the count bound enforced", len(file.Episodes))
	}

	old := OpenLedger(t.TempDir())
	oldNow := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	old.now = func() time.Time { return oldNow }
	e := episode("t1", "g1")
	if err := old.MarkRequested([]Episode{e}); err != nil {
		t.Fatal(err)
	}
	old.now = func() time.Time { return oldNow.Add(MaxLedgerAge + time.Minute) }
	if eligible := old.Eligible([]Episode{e}); len(eligible) != 1 {
		t.Fatal("an aged-out entry loses its mechanism memory and may wake again")
	}
	if _, exists := old.read().Episodes[e.Key()]; exists {
		t.Fatal("aged entries are pruned, not merely re-eligibled")
	}
}

func TestBridgeErrorCooldownBoundsFailureNotices(t *testing.T) {
	ledger, now := testLedger(t)
	host := "claude"
	if !ledger.BridgeErroredBefore(host, BridgeFailureCooldown) {
		t.Fatal("no recorded failure yet")
	}
	if err := ledger.MarkBridgeError(host, "arm failed"); err != nil {
		t.Fatal(err)
	}
	if ledger.BridgeErroredBefore(host, BridgeFailureCooldown) {
		t.Fatal("a fresh failure notice suppresses duplicates inside the cooldown")
	}
	*now = now.Add(BridgeFailureCooldown + time.Minute)
	if !ledger.BridgeErroredBefore(host, BridgeFailureCooldown) {
		t.Fatal("the cooldown re-arms so a still-broken bridge surfaces again")
	}
}

func TestProgressDistinguishesAcceptanceFromOrientation(t *testing.T) {
	ledger, _ := testLedger(t)
	e := episode("t1", "g1")
	lastAccepted, lastOriented := ledger.Progress()
	if lastAccepted != nil || lastOriented != nil {
		t.Fatal("empty ledger reports no stamps")
	}
	if err := ledger.MarkAccepted(Keys([]Episode{e})); err != nil {
		t.Fatal(err)
	}
	lastAccepted, lastOriented = ledger.Progress()
	if lastAccepted == nil || lastOriented != nil {
		t.Fatal("acceptance alone must not read as orientation progress")
	}
	if err := ledger.MarkOriented([]Episode{e}); err != nil {
		t.Fatal(err)
	}
	if _, lastOriented = ledger.Progress(); lastOriented == nil {
		t.Fatal("orientation stamp missing after orient")
	}
}

func TestConcurrentLedgerWritersStayConsistent(t *testing.T) {
	ledger := OpenLedger(t.TempDir())
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e := episode(string(rune('a'+i)), "g")
			_ = ledger.MarkRequested([]Episode{e})
			_ = ledger.MarkOriented([]Episode{e})
		}(i)
	}
	wg.Wait()
	file := ledger.read()
	if len(file.Episodes) != 8 {
		t.Fatalf("entries = %d, want every concurrent writer's episode recorded once", len(file.Episodes))
	}
	for _, record := range file.Episodes {
		if record.Oriented == nil || record.DeliveryRequested == nil {
			t.Fatalf("record %#v lost a stage to a racing writer", record.Key)
		}
	}
}
