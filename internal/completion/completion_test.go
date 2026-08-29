package completion

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"pgregory.net/rapid"
)

// What Append stamps onto every record it writes, so a test can say what it appended and still
// compare against what a reader gets back.
func appended(r Record) Record {
	r.Version = RecordVersion
	if r.ProjectID == "" {
		r.ProjectID = ProjectIDUnknown
	}
	return r
}

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
		if got[i] != appended(want[i]) {
			t.Fatalf("record %d = %+v, want %+v", i, got[i], appended(want[i]))
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

// The storage half of atqamz/hand#61's "uncapped, or capped somewhere other
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
		if got[i] != appended(want[i]) {
			t.Fatalf("record %d = %+v, want %+v", i, got[i], appended(want[i]))
		}
	}
}

// The concurrency guarantee the brief for atqamz/hand#61 requires explaining: two Append calls
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

func writeStore(t *testing.T, dir string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(Path(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(dir), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readStore(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The fixture atqamz/hand#388 is about: several projects, one of them removed with its name since
// claimed by a different project, and records whose lineage the file alone cannot settle. The
// unresolvable ones have to stay visibly unresolved rather than join the wrong project's history.
func TestMigrateProjectIdentityMarksUnresolvableRecordsUnknown(t *testing.T) {
	dir := t.TempDir()
	writeStore(t, dir,
		`{"id":"alpha-1","project":"alpha","kind":"ship","outcome":"merged","detail":"","torndown_at":"2026-08-01T00:00:00Z"}`,
		`{"id":"beta-1","project":"beta","kind":"scout","outcome":"done","detail":"","torndown_at":"2026-08-02T00:00:00Z"}`,
		`{"id":"gamma-old","project":"gamma","kind":"ship","outcome":"merged","detail":"","torndown_at":"2026-08-03T00:00:00Z"}`,
		`{"id":"gamma-new","project":"gamma","kind":"ship","outcome":"merged","detail":"","torndown_at":"2026-08-04T00:00:00Z"}`,
	)
	// alpha is registered; beta was removed and its name never reclaimed; gamma was removed and its
	// name taken by a later project, whose own task row is the only thing that can tell the two
	// gamma records apart.
	live := map[string]string{"alpha": "p_alpha", "gamma": "p_gamma_two"}
	byTask := map[string]string{"gamma-new": "p_gamma_two"}
	resolve := func(r Record) string {
		if id, ok := byTask[r.ID]; ok {
			return id
		}
		return live[r.Project]
	}

	if err := MigrateProjectIdentity(dir, resolve); err != nil {
		t.Fatal(err)
	}

	records, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		id        string
		project   string
		projectID string
	}{
		{"alpha-1", "alpha", "p_alpha"},
		{"beta-1", "beta", ProjectIDUnknown},
		{"gamma-old", "gamma", "p_gamma_two"},
		{"gamma-new", "gamma", "p_gamma_two"},
	}
	if len(records) != len(want) {
		t.Fatalf("records = %+v, want %d", records, len(want))
	}
	for i, w := range want {
		got := records[i]
		if got.ID != w.id || got.Project != w.project || got.ProjectID != w.projectID || got.Version != RecordVersion {
			t.Fatalf("record %d = %+v, want id=%q project=%q project_id=%q version=%d", i, got, w.id, w.project, w.projectID, RecordVersion)
		}
	}
}

// A resolver that can name nothing must leave every record explicitly unknown rather than empty:
// the whole point of the marker is that an ambiguous record reads as ambiguous.
func TestMigrateProjectIdentityNeverGuessesFromTheNameAlone(t *testing.T) {
	dir := t.TempDir()
	writeStore(t, dir,
		`{"id":"one","project":"retired","kind":"ship","outcome":"merged","detail":"","torndown_at":"2026-08-01T00:00:00Z"}`,
	)
	if err := MigrateProjectIdentity(dir, func(Record) string { return "" }); err != nil {
		t.Fatal(err)
	}
	records, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ProjectID != ProjectIDUnknown || records[0].Project != "retired" {
		t.Fatalf("records = %+v, want the name preserved and the identity marked unknown", records)
	}
}

func TestMigrateProjectIdentityIsIdempotentAndLeavesDamagedLinesAlone(t *testing.T) {
	dir := t.TempDir()
	damaged := `{"id":"damag`
	writeStore(t, dir,
		`{"id":"alpha-1","project":"alpha","kind":"ship","outcome":"merged","detail":"","torndown_at":"2026-08-01T00:00:00Z"}`,
		damaged,
		`{"id":"alpha-2","project":"alpha","kind":"ship","outcome":"merged","detail":"","torndown_at":"2026-08-02T00:00:00Z"}`,
	)
	resolve := func(Record) string { return "p_alpha" }
	if err := MigrateProjectIdentity(dir, resolve); err != nil {
		t.Fatal(err)
	}
	first := readStore(t, dir)
	if !strings.Contains(first, damaged+"\n") {
		t.Fatalf("store = %q, want the damaged line carried through verbatim", first)
	}

	// A second run resolves to a different identity on purpose: an already-migrated record must be
	// passed through untouched rather than backfilled again.
	if err := MigrateProjectIdentity(dir, func(Record) string { return "p_other" }); err != nil {
		t.Fatal(err)
	}
	if second := readStore(t, dir); second != first {
		t.Fatalf("store = %q after a second migration, want %q", second, first)
	}
}

// Nothing is written when there is nothing to migrate, so a re-run cannot disturb the file's mode
// or its contents, and a store that does not exist yet is simply absent rather than an error.
func TestMigrateProjectIdentityWritesNothingWhenThereIsNothingToDo(t *testing.T) {
	dir := t.TempDir()
	if err := MigrateProjectIdentity(dir, func(Record) string { return "p_alpha" }); err != nil {
		t.Fatalf("missing store: %v", err)
	}
	if _, err := os.Stat(Path(dir)); !os.IsNotExist(err) {
		t.Fatalf("stat = %v, want the migration to have created nothing", err)
	}

	if err := Append(dir, Record{ID: "one", Project: "alpha", ProjectID: "p_alpha"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	content := readStore(t, dir)
	if err := MigrateProjectIdentity(dir, func(Record) string { return "p_other" }); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if readStore(t, dir) != content || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("store changed: %q at %v, want %q at %v", readStore(t, dir), after.ModTime(), content, before.ModTime())
	}
}

func recordGen() *rapid.Generator[Record] {
	return rapid.Custom(func(t *rapid.T) Record {
		return Record{
			Project:          rapid.String().Draw(t, "project"),
			ProjectID:        rapid.OneOf(rapid.Just(""), rapid.StringMatching(`p_[0-9a-f]{4,8}`)).Draw(t, "project-id"),
			Kind:             rapid.String().Draw(t, "kind"),
			Outcome:          rapid.String().Draw(t, "outcome"),
			Detail:           rapid.String().Draw(t, "detail"),
			TornDownAt:       rapid.String().Draw(t, "torndown-at"),
			AttemptID:        rapid.Int64Range(0, 1000).Draw(t, "attempt-id"),
			AttemptLifecycle: rapid.String().Draw(t, "attempt-lifecycle"),
		}
	})
}

// INV-PROJ-4: a completion record keeps the label it was written with; it is an audit line, not
// a view. Appends records with arbitrary labels and checks every one reads back with its own
// label untouched by anything appended after it, never re-derived from anything else.
func TestCompletionRecordsRoundTripTheirWrittenLabelAcrossArbitraryAppends(t *testing.T) {
	dir := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		_ = os.Remove(Path(dir))

		n := rapid.IntRange(1, 8).Draw(t, "count")
		records := make([]Record, n)
		for i := range records {
			r := recordGen().Draw(t, fmt.Sprintf("record-%d", i))
			r.ID = fmt.Sprintf("rec-%d", i)
			records[i] = r
		}
		for _, r := range records {
			if err := Append(dir, r); err != nil {
				t.Fatalf("Append(%+v): %v", r, err)
			}
		}

		got, err := List(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(records) {
			t.Fatalf("List returned %d records, want %d", len(got), len(records))
		}
		for i, want := range records {
			if got[i] != appended(want) {
				t.Fatalf("record %d = %+v, want %+v", i, got[i], appended(want))
			}
		}
	})
}

// INV-PROJ-5 (content half) and INV-PROJ-6 together: the migration rewrites only lines below
// the current record version, resolving identity through the resolver alone - an empty answer
// always becomes ProjectIDUnknown - while every current or unparseable line passes through as is.
func TestMigrateProjectIdentityRewritesOnlyLegacyLinesAndNeverGuessesAnUnresolvedLineage(t *testing.T) {
	dir := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		_ = os.RemoveAll(filepath.Dir(Path(dir)))

		type expectation struct {
			untouched bool
			record    Record
		}

		n := rapid.IntRange(1, 6).Draw(t, "lines")
		inputLines := make([]string, n)
		expectations := make([]expectation, n)
		resolved := map[string]string{}

		for i := 0; i < n; i++ {
			switch rapid.SampledFrom([]string{"legacy", "current", "garbage"}).Draw(t, fmt.Sprintf("kind-%d", i)) {
			case "garbage":
				// Deliberately unterminated JSON, so it can never accidentally decode as a
				// low-version record and get migrated instead of passed through.
				suffix := rapid.StringMatching(`[a-z0-9]{0,12}`).Draw(t, fmt.Sprintf("garbage-%d", i))
				inputLines[i] = `{"id":"` + suffix
				expectations[i] = expectation{untouched: true}
			case "current":
				r := recordGen().Draw(t, fmt.Sprintf("current-%d", i))
				r.ID = fmt.Sprintf("rec-%d", i)
				r.Version = RecordVersion
				if r.ProjectID == "" {
					r.ProjectID = ProjectIDUnknown
				}
				data, err := json.Marshal(r)
				if err != nil {
					t.Fatal(err)
				}
				inputLines[i] = string(data)
				expectations[i] = expectation{untouched: true}
			case "legacy":
				r := recordGen().Draw(t, fmt.Sprintf("legacy-%d", i))
				r.ID = fmt.Sprintf("rec-%d", i)
				r.Version = 0
				answer := rapid.OneOf(rapid.Just(""), rapid.StringMatching(`p_[0-9a-f]{4,8}`)).Draw(t, fmt.Sprintf("resolved-%d", i))
				resolved[r.ID] = answer
				data, err := json.Marshal(r)
				if err != nil {
					t.Fatal(err)
				}
				inputLines[i] = string(data)
				want := r
				want.Version = RecordVersion
				want.ProjectID = answer
				if want.ProjectID == "" {
					want.ProjectID = ProjectIDUnknown
				}
				expectations[i] = expectation{record: want}
			}
		}

		if err := os.MkdirAll(filepath.Dir(Path(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(Path(dir), []byte(strings.Join(inputLines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		resolve := func(r Record) string { return resolved[r.ID] }
		if err := MigrateProjectIdentity(dir, resolve); err != nil {
			t.Fatal(err)
		}

		after, err := os.ReadFile(Path(dir))
		if err != nil {
			t.Fatal(err)
		}
		gotLines := strings.Split(strings.TrimSuffix(string(after), "\n"), "\n")
		if len(gotLines) != n {
			t.Fatalf("line count after migration = %d, want %d", len(gotLines), n)
		}
		for i, exp := range expectations {
			if exp.untouched {
				if gotLines[i] != inputLines[i] {
					t.Fatalf("line %d changed though it should pass through untouched: before %q, after %q", i, inputLines[i], gotLines[i])
				}
				continue
			}
			var got Record
			if err := json.Unmarshal([]byte(gotLines[i]), &got); err != nil {
				t.Fatalf("line %d did not decode after migration: %v", i, err)
			}
			if got != exp.record {
				t.Fatalf("line %d = %+v, want %+v", i, got, exp.record)
			}
		}
	})
}

// INV-PROJ-5 (rollback half): the rewrite fully replaces the store or leaves it exactly as it
// was, since atomicfile.Write stages the new content in a temp file before renaming over the
// original. Forces temp-file creation to fail; unix-only, like notice_test.go's equivalent.
func TestMigrateProjectIdentityLeavesTheStoreExactlyAsItWasWhenTheReplaceFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not enforce directory write permissions the same way")
	}
	dir := t.TempDir()
	stateDir := filepath.Dir(Path(dir))

	rapid.Check(t, func(t *rapid.T) {
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(stateDir, 0o755); err != nil {
			t.Fatal(err)
		}

		n := rapid.IntRange(1, 4).Draw(t, "lines")
		lines := make([]string, n)
		for i := range lines {
			r := recordGen().Draw(t, fmt.Sprintf("record-%d", i))
			r.ID = fmt.Sprintf("rec-%d", i)
			r.Version = 0
			data, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			lines[i] = string(data)
		}
		if err := os.WriteFile(Path(dir), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(Path(dir))
		if err != nil {
			t.Fatal(err)
		}

		if err := os.Chmod(stateDir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(stateDir, 0o755) })

		if err := MigrateProjectIdentity(dir, func(Record) string { return "p_should_not_land" }); err == nil {
			t.Fatal("MigrateProjectIdentity succeeded despite an unwritable state directory")
		}

		if err := os.Chmod(stateDir, 0o755); err != nil {
			t.Fatal(err)
		}
		after, err := os.ReadFile(Path(dir))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(after, before) {
			t.Fatalf("store changed despite a failed replace: before %q, after %q", before, after)
		}
	})
}

// FindAttempt is completion_test.go's own suite's responsibility to exercise directly
// (atqamz/hand#498): internal/runtime's tests only reach it indirectly as a teardown-recovery
// dependency, which left it at 0% coverage from this package's own suite despite being live code.
func TestFindAttemptFindsTheMatchingRecordAmongOthers(t *testing.T) {
	dir := t.TempDir()
	records := []Record{
		{ID: "one", AttemptID: 10, Kind: "ship", Outcome: "merged"},
		{ID: "two", AttemptID: 20, Kind: "scout", Outcome: "done"},
		{ID: "three", AttemptID: 30, Kind: "ship", Outcome: "merged"},
	}
	for _, r := range records {
		if err := Append(dir, r); err != nil {
			t.Fatal(err)
		}
	}

	record, found, err := FindAttempt(dir, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !found || record != appended(records[1]) {
		t.Fatalf("FindAttempt(20) = %+v, found=%v, want %+v, found=true", record, found, appended(records[1]))
	}
}

func TestFindAttemptReportsAbsentAttemptWithoutError(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, Record{ID: "one", AttemptID: 10}); err != nil {
		t.Fatal(err)
	}

	record, found, err := FindAttempt(dir, 99)
	if err != nil {
		t.Fatal(err)
	}
	if found || record != (Record{}) {
		t.Fatalf("FindAttempt(99) = %+v, found=%v, want zero value, found=false", record, found)
	}
}

func TestFindAttemptMissingStoreReportsAbsentWithoutError(t *testing.T) {
	dir := t.TempDir()
	record, found, err := FindAttempt(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if found || record != (Record{}) {
		t.Fatalf("FindAttempt on a missing store = %+v, found=%v, want zero value, found=false", record, found)
	}
}

// Mirrors TestListSkipsDamagedLine: a truncated write leaves one unparseable line, and the
// attempt recorded after it must still be reachable.
func TestFindAttemptSkipsADamagedLineAndFindsWhatFollows(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, Record{ID: "before", AttemptID: 1}); err != nil {
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

	after := Record{ID: "after", AttemptID: 2}
	if err := Append(dir, after); err != nil {
		t.Fatal(err)
	}

	record, found, err := FindAttempt(dir, 2)
	if err != nil {
		t.Fatalf("FindAttempt failed on a store with one damaged line: %v", err)
	}
	if !found || record != appended(after) {
		t.Fatalf("FindAttempt(2) = %+v, found=%v, want %+v, found=true", record, found, appended(after))
	}
}

// FindAttempt's doc comment says an absent AttemptID is intentionally never inferred. Proves the
// guard actually short-circuits: this store holds a record whose own AttemptID decodes to the
// zero value an unset field reads back as, and would satisfy the scan loop's equality check without it.
func TestFindAttemptZeroNeverMatchesEvenARecordThatWouldOtherwiseQualify(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, Record{ID: "no-attempt", Kind: "scout", Outcome: "done"}); err != nil {
		t.Fatal(err)
	}

	record, found, err := FindAttempt(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if found || record != (Record{}) {
		t.Fatalf("FindAttempt(dir, 0) = %+v, found=%v, want zero value, found=false", record, found)
	}
}

// The value scanner.Buffer caps List and FindAttempt's line length at (completion.go:175, 209);
// mirrored here rather than imported since it is a local literal, not an exported constant.
const scannerMaxTokenSize = 1024 * 1024

// Returns r with Detail set so the encoded JSON line is exactly target bytes once Append stamps
// it, measured from r's own real encoding rather than a guessed fixed overhead. r must go
// through Append unmodified afterwards or the byte count this anticipates will not match.
func paddedRecord(t *testing.T, r Record, target int) Record {
	t.Helper()
	r = appended(r)
	r.Detail = ""
	probe, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	pad := target - len(probe)
	if pad < 0 {
		t.Fatalf("record already encodes to %d bytes without Detail, want %d", len(probe), target)
	}
	r.Detail = strings.Repeat("x", pad)
	final, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(final) != target {
		t.Fatalf("padded record encodes to %d bytes, want %d", len(final), target)
	}
	return r
}

// atqamz/hand#498: nothing wrote a record near this bound before, so neither scanner.Buffer
// argument was pinned. One byte under the limit is the largest line the scanner accepts at all.
func TestRoundTripsARecordOneByteUnderTheScannerLimit(t *testing.T) {
	dir := t.TempDir()
	r := paddedRecord(t, Record{ID: "huge", AttemptID: 777}, scannerMaxTokenSize-1)
	if err := Append(dir, r); err != nil {
		t.Fatal(err)
	}

	got, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0] != appended(r) {
		t.Fatalf("List = %+v, want [%+v]", got, appended(r))
	}

	record, found, err := FindAttempt(dir, 777)
	if err != nil {
		t.Fatalf("FindAttempt: %v", err)
	}
	if !found || record != appended(r) {
		t.Fatalf("FindAttempt(777) = %+v, found=%v, want %+v, found=true", record, found, appended(r))
	}
}

// At exactly the limit, List and FindAttempt fail loudly and specifically (INV-PROJ-8): the error
// names bufio.ErrTooLong, the store's path, and the failing line's position, rather than reading
// like the same generic failure every other I/O error produces - never truncated, never skipped.
func TestSurfacesTheReadErrorForARecordAtTheScannerLimit(t *testing.T) {
	dir := t.TempDir()
	r := paddedRecord(t, Record{ID: "huge", AttemptID: 777}, scannerMaxTokenSize)
	if err := Append(dir, r); err != nil {
		t.Fatal(err)
	}

	_, err := List(dir)
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("List error = %v, want it to wrap bufio.ErrTooLong", err)
	}
	if !strings.Contains(err.Error(), Path(dir)) || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("List error = %q, want it to name the store path and line 1", err)
	}

	_, _, err = FindAttempt(dir, 777)
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("FindAttempt error = %v, want it to wrap bufio.ErrTooLong", err)
	}
	if !strings.Contains(err.Error(), Path(dir)) || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("FindAttempt error = %q, want it to name the store path and line 1", err)
	}
}

// FindAttempt keeps its own copy of the same scan loop, so a line before the oversized one has to
// advance FindAttempt's own count, not just List's (TestListLosesPrecedingRecordsWhenALaterLine...).
func TestFindAttemptSurfacesTheReadErrorNamingTheRealFileLine(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, Record{ID: "before", AttemptID: 1}); err != nil {
		t.Fatal(err)
	}
	huge := paddedRecord(t, Record{ID: "huge", AttemptID: 2}, scannerMaxTokenSize)
	if err := Append(dir, huge); err != nil {
		t.Fatal(err)
	}

	_, _, err := FindAttempt(dir, 2)
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("FindAttempt error = %v, want it to wrap bufio.ErrTooLong", err)
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("FindAttempt error = %q, want it to name line 2, not the good line before it", err)
	}
}

// Confirms the line position named in the error is the real 1-indexed line in
// state/completions.jsonl - blank and damaged lines consumed by the scanner included - not just a
// count of valid records, since repairing the store by hand needs the real line to open and edit.
func TestSurfacesTheReadErrorNamingTheRealFileLineAcrossOtherLinesBeforeIt(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, Record{ID: "one", AttemptID: 1}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(Path(dir), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// Line 2 is blank, line 3 is damaged (mirrors TestListSkipsDamagedLine): both are lines the
	// scanner consumes without producing a record, so the huge line that follows is really line 4.
	if _, err := f.WriteString("\n{\"id\":\"damag\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	huge := paddedRecord(t, Record{ID: "huge", AttemptID: 2}, scannerMaxTokenSize)
	if err := Append(dir, huge); err != nil {
		t.Fatal(err)
	}

	_, err = List(dir)
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("List error = %v, want it to wrap bufio.ErrTooLong", err)
	}
	if !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("List error = %q, want it to name line 4 (1 record, 1 blank, 1 damaged, then the huge line)", err)
	}
}

// The failure is total, not local to the oversized line (INV-PROJ-8): unlike
// TestListSkipsDamagedLine's partial-write case, List does not skip the oversized line and hand
// back everything else - a record written before it becomes unreadable too.
func TestListLosesPrecedingRecordsWhenALaterLineExceedsTheScannerLimit(t *testing.T) {
	dir := t.TempDir()
	small := Record{ID: "before", AttemptID: 1}
	if err := Append(dir, small); err != nil {
		t.Fatal(err)
	}
	huge := paddedRecord(t, Record{ID: "huge", AttemptID: 2}, scannerMaxTokenSize)
	if err := Append(dir, huge); err != nil {
		t.Fatal(err)
	}

	got, err := List(dir)
	if !errors.Is(err, bufio.ErrTooLong) || got != nil {
		t.Fatalf("List = %+v, err %v, want nil and a wrapped bufio.ErrTooLong even though %q came first", got, err, small.ID)
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("List error = %q, want it to name line 2 - the huge line, not the good one before it", err)
	}
}
