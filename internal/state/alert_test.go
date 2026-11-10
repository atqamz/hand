package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAVersion21DatabaseOnDiskMigratesToTheAlertTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{
		schema, migrations[0], migrations[1], migrations[2], migrations[3], migrations[4], `PRAGMA user_version = 21`,
		`INSERT INTO project(name, repo, created_at) VALUES ('hand', '/home/me/hand', '2026-09-26T00:00:00Z')`,
		`INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'hand', 'one', '', 'inbox', '2026-09-26T00:00:00Z', '2026-09-26T00:00:00Z')`,
	} {
		if _, err := db.Exec(m); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		s, err := Open(path, clock)
		if err != nil {
			t.Fatal(err)
		}
		var v, n int
		if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != SchemaVersion {
			t.Fatalf("user_version = %d, %v", v, err)
		}
		if err := s.db.QueryRow(`SELECT count(*) FROM alert`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("alert rows = %d, %v", n, err)
		}
		if tasks, err := s.Tasks(context.Background(), nil, 10); err != nil || len(tasks) != 1 {
			t.Fatalf("tasks = %+v, %v", tasks, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAMigratedDatabaseCarriesTheCurrentSchemaInItsHeader(t *testing.T) {
	s, path := openTest(t)
	if err := s.RecordAlert(context.Background(), "attempt.failed", "a1", "a1 failed", ""); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	v, ok := headerVersion(path)
	if !ok || v != SchemaVersion {
		t.Fatalf("header version = %d, %v", v, ok)
	}
}

func alertStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	s, _ := openTest(t)
	now := fixed
	s.now = func() time.Time { return now }
	return s, &now
}

func TestRecordAlertKeepsTheFieldsAndFindsTheTask(t *testing.T) {
	s, _ := alertStore(t)
	ctx := context.Background()
	seedProject(t, s)
	task, err := s.AddTask(ctx, "hand", "task", "")
	if err != nil {
		t.Fatal(err)
	}
	long := "x\x1b[31m" + strings.Repeat("é", 300) + "\nline"
	for _, a := range [][3]string{
		{"attempt.failed", "a9", "a9 failed"},
		{"decision.asked", TaskRef(task.ID), long},
		{"supervisor.stalled", "supervisor", "idle"},
	} {
		if err := s.RecordAlert(ctx, a[0], a[1], a[2], "handboard://open/f0123456789ab/"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.AlertsAfter(ctx, 0, 50)
	if err != nil || len(got) != 3 {
		t.Fatalf("alerts = %+v, %v", got, err)
	}
	if got[0].Seq != 1 || got[1].Seq != 2 || got[2].Seq != 3 || got[0].Kind != "attempt.failed" || got[0].TaskID != 0 || got[1].TaskID != task.ID || got[2].TaskID != 0 || got[2].Ref != "supervisor" {
		t.Fatalf("alerts = %+v", got)
	}
	if n := len([]rune(got[1].Text)); n != MaxAlertText || strings.ContainsAny(got[1].Text, "\x1b\n") {
		t.Fatalf("text = %q (%d runes)", got[1].Text, n)
	}
	if got[0].Link != "handboard://open/f0123456789ab/" || !got[0].At.Equal(fixed) || !got[0].FetchedAt.IsZero() || !got[0].AckedAt.IsZero() {
		t.Fatalf("alert = %+v", got[0])
	}
}

func TestAlertsAfterIsOrderedCappedAndWithinTheWindow(t *testing.T) {
	s, now := alertStore(t)
	ctx := context.Background()
	record := func(n int) {
		for range n {
			if err := s.RecordAlert(ctx, "attempt.failed", "a1", "a1 failed", ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	record(2)
	*now = now.Add(23 * time.Hour)
	record(60)
	*now = now.Add(2 * time.Hour)
	got, err := s.AlertsAfter(ctx, 0, 51)
	if err != nil || len(got) != 51 || got[0].Seq != 3 || got[50].Seq != 53 {
		t.Fatalf("first %d alerts, %v", len(got), err)
	}
	got, err = s.AlertsAfter(ctx, 50, 51)
	if err != nil || len(got) != 12 || got[0].Seq != 51 || got[11].Seq != 62 {
		t.Fatalf("after 50: %d alerts, %v", len(got), err)
	}
	if got, err = s.AlertsAfter(ctx, 62, 51); err != nil || len(got) != 0 {
		t.Fatalf("after 62: %+v, %v", got, err)
	}
}

func TestAlertsAreKeptSevenDaysAndTheSequenceNeverRepeats(t *testing.T) {
	s, now := alertStore(t)
	ctx := context.Background()
	for range 3 {
		if err := s.RecordAlert(ctx, "attempt.failed", "a1", "a1 failed", ""); err != nil {
			t.Fatal(err)
		}
	}
	*now = now.Add(AlertKeep - time.Minute)
	if err := s.RecordAlert(ctx, "attempt.failed", "a1", "fresh", ""); err != nil {
		t.Fatal(err)
	}
	if all, err := s.Alerts(ctx); err != nil || len(all) != 4 {
		t.Fatalf("alerts = %d, %v", len(all), err)
	}
	*now = now.Add(2 * time.Minute)
	if err := s.RecordAlert(ctx, "attempt.failed", "a1", "later", ""); err != nil {
		t.Fatal(err)
	}
	all, err := s.Alerts(ctx)
	if err != nil || len(all) != 2 || all[0].Seq != 5 || all[1].Text != "fresh" {
		t.Fatalf("alerts = %+v, %v", all, err)
	}
	*now = now.Add(30 * 24 * time.Hour)
	if _, err := s.db.Exec(`DELETE FROM alert`); err != nil {
		t.Fatal(err)
	}
	if seq, err := s.LatestAlertSeq(ctx); err != nil || seq != 5 {
		t.Fatalf("latest after pruning everything = %d, %v", seq, err)
	}
	if err := s.RecordAlert(ctx, "attempt.failed", "a1", "next", ""); err != nil {
		t.Fatal(err)
	}
	if all, _ = s.Alerts(ctx); len(all) != 1 || all[0].Seq != 6 {
		t.Fatalf("alerts = %+v", all)
	}
}

func TestAlertsListsOnlyTheLastSevenDaysWithoutANewInsert(t *testing.T) {
	s, now := alertStore(t)
	ctx := context.Background()
	for range 2 {
		if err := s.RecordAlert(ctx, "attempt.failed", "a1", "old", ""); err != nil {
			t.Fatal(err)
		}
	}
	*now = now.Add(AlertKeep - time.Minute)
	if err := s.RecordAlert(ctx, "attempt.failed", "a1", "fresh", ""); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Minute)
	all, err := s.Alerts(ctx)
	if err != nil || len(all) != 1 || all[0].Text != "fresh" {
		t.Fatalf("alerts = %+v, %v", all, err)
	}
	var stored int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM alert`).Scan(&stored); err != nil || stored != 3 {
		t.Fatalf("stored = %d, %v", stored, err)
	}
}

func TestLatestAlertSeqIsZeroWithNoAlerts(t *testing.T) {
	s, _ := alertStore(t)
	if seq, err := s.LatestAlertSeq(context.Background()); err != nil || seq != 0 {
		t.Fatalf("latest = %d, %v", seq, err)
	}
}

func TestAlertReceiptsFollowFetchThenAck(t *testing.T) {
	s, now := alertStore(t)
	ctx := context.Background()
	for range 3 {
		if err := s.RecordAlert(ctx, "attempt.failed", "a1", "a1 failed", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AckAlerts(ctx, 3); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(2 * time.Second)
	if err := s.FetchedAlerts(ctx, 1, 2); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(3 * time.Second)
	if err := s.AckAlerts(ctx, 2); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	if err := s.AckAlerts(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if err := s.FetchedAlerts(ctx, 1, 3); err != nil {
		t.Fatal(err)
	}
	all, err := s.Alerts(ctx)
	if err != nil || len(all) != 3 {
		t.Fatalf("alerts = %+v, %v", all, err)
	}
	third, second, first := all[0], all[1], all[2]
	if !first.FetchedAt.Equal(fixed.Add(2*time.Second)) || !first.AckedAt.Equal(fixed.Add(5*time.Second)) || !second.AckedAt.Equal(fixed.Add(5*time.Second)) {
		t.Fatalf("first = %+v, second = %+v", first, second)
	}
	if d, ok := first.Delay(); !ok || d != 5*time.Second {
		t.Fatalf("delay = %v, %v", d, ok)
	}
	if !third.AckedAt.IsZero() || !third.FetchedAt.Equal(fixed.Add(65*time.Second)) {
		t.Fatalf("an unfetched alert was acked: %+v", third)
	}
	if _, ok := third.Delay(); ok {
		t.Fatal("an unacked alert has a delay")
	}
}
