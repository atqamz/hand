package store

import "testing"

func TestClassifyLegacyV18CutoverDurableStateAllowsHistoricalUsageLimitStuckMarker(t *testing.T) {
	home := createLegacyV18DurableQuiescenceFixture(t)
	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	execLegacyV18DurableQuiescenceTest(t, db, `UPDATE attempt SET usage_limit_episode = 3, usage_limit_stuck_episode = 3, usage_limit_retry_at = '' WHERE task_id = 'task-1'`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := classifyLegacyV18DurableQuiescenceTestHome(t, home); err != nil {
		t.Fatalf("historical usage-limit stuck marker blocked cutover: %v", err)
	}
}
