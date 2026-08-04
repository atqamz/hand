package completion

import (
	"fmt"
	"os"
	"sync"
	"testing"
)

func TestAppendListRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := []Record{
		{ID: "fix-login", Project: "nsr", Kind: "ship", Outcome: "merged", Detail: "PR https://example.com/pull/1", TornDownAt: "2026-08-02T13:00:00Z"},
		{ID: "audit-deps", Project: "nsr", Kind: "scout", Outcome: "done", Detail: "report data/audit-deps/report.md", TornDownAt: "2026-08-02T13:05:00Z"},
	}
	for _, r := range want {
		if err := Append(dir, r); err != nil {
			t.Fatal(err)
		}
	}

	got, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("List returned %d records, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("record %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestListMissingStore(t *testing.T) {
	dir := t.TempDir()
	got, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("List on a missing store = %+v, want nil", got)
	}
}

// The storage half of atqamz/secondhand#61's "uncapped, or capped somewhere other
// than the display layer" requirement.
func TestAppendUncapped(t *testing.T) {
	dir := t.TempDir()
	const n = 25
	for i := 0; i < n; i++ {
		if err := Append(dir, Record{ID: fmt.Sprintf("task-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != n {
		t.Fatalf("List returned %d records, want %d - store must stay uncapped", len(got), n)
	}
}

// Covers the read semantic a durable store needs: a truncated write leaves one
// unparseable line, and every good record around it must still be readable.
func TestListSkipsDamagedLine(t *testing.T) {
	dir := t.TempDir()
	first := Record{ID: "before", Project: "nsr", Kind: "ship", Outcome: "merged", Detail: "PR 1", TornDownAt: "2026-08-02T13:00:00Z"}
	last := Record{ID: "after", Project: "nsr", Kind: "ship", Outcome: "merged", Detail: "PR 2", TornDownAt: "2026-08-02T13:10:00Z"}
	if err := Append(dir, first); err != nil {
		t.Fatal(err)
	}

	f, err := os.OpenFile(Path(dir), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"id\":\"damag\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if err := Append(dir, last); err != nil {
		t.Fatal(err)
	}

	got, err := List(dir)
	if err != nil {
		t.Fatalf("List failed on a store with one damaged line: %v", err)
	}
	want := []Record{first, last}
	if len(got) != len(want) {
		t.Fatalf("List returned %d records, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("record %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// The concurrency guarantee the brief for atqamz/secondhand#61 requires explaining: two Append calls
// racing must never lose either line, unlike state/events.log's read-modify-write pattern over the
// whole file.
func TestAppendConcurrent(t *testing.T) {
	dir := t.TempDir()
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := Append(dir, Record{ID: fmt.Sprintf("task-%d", i)}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()

	got, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != n {
		t.Fatalf("List returned %d records after %d concurrent appends, want %d - a race dropped one", len(got), n, n)
	}
	seen := make(map[string]bool, n)
	for _, r := range got {
		if seen[r.ID] {
			t.Fatalf("record %q appeared more than once", r.ID)
		}
		seen[r.ID] = true
	}
}
