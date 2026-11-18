package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var dropHistoryIndex = []string{`DROP TRIGGER supervisor_input_fts_insert`, `DROP TRIGGER supervisor_input_fts_delete`, `DROP TRIGGER supervisor_input_fts_update`, `DROP TRIGGER report_fts_insert`, `DROP TRIGGER report_fts_delete`, `DROP TRIGGER report_fts_update`, `DROP TRIGGER supervisor_reply_fts_insert`, `DROP TRIGGER supervisor_reply_fts_delete`, `DROP TRIGGER supervisor_reply_fts_update`, `DROP TABLE supervisor_input_fts`, `DROP TABLE report_fts`, `DROP TABLE supervisor_reply_fts`}

type historyDB struct {
	t       *Store
	sup     Supervisor
	attempt Attempt
}

func newHistory(t *testing.T) *historyDB {
	t.Helper()
	s, _ := openTest(t)
	a := runningAttempt(t, s)
	sup, err := s.AddSupervisor(context.Background(), SupervisorSpec{Harness: "claude", Argv: []string{"claude"}, Session: "sess1"})
	if err != nil {
		t.Fatal(err)
	}
	return &historyDB{t: s, sup: sup, attempt: a}
}

func (h *historyDB) exec(t *testing.T, q string, args ...any) int64 {
	t.Helper()
	res, err := h.t.db.Exec(q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (h *historyDB) input(t *testing.T, at, body string) string {
	t.Helper()
	return fmt.Sprintf("i%d", h.exec(t, `INSERT INTO supervisor_input(body, created_at) VALUES (?, ?)`, body, at))
}

func (h *historyDB) reply(t *testing.T, at string, n int, body string) string {
	t.Helper()
	return fmt.Sprintf("y%d", h.exec(t, `INSERT INTO supervisor_reply(supervisor_id, session, at, n, body) VALUES (?, 'sess1', ?, ?, ?)`, h.sup.ID, at, n, body))
}

func (h *historyDB) report(t *testing.T, task int64, at, body string) string {
	t.Helper()
	return fmt.Sprintf("r%d", h.exec(t, `INSERT INTO report(attempt_id, task_id, status, body, created_at) VALUES (?, ?, 'done', ?, ?)`, h.attempt.ID, task, body, at))
}

func (h *historyDB) search(t *testing.T, q HistoryQuery) HistoryResult {
	t.Helper()
	if q.Limit == 0 {
		q.Limit = 5
	}
	res, err := h.t.SearchHistory(context.Background(), q)
	if err != nil {
		t.Fatalf("SearchHistory(%+v): %v", q, err)
	}
	return res
}

func refs(res HistoryResult) []string {
	out := []string{}
	for _, h := range res.Hits {
		out = append(out, h.Ref)
	}
	slices.Sort(out)
	return out
}

func TestFTS5IsInTheBuild(t *testing.T) {
	s, _ := openTest(t)
	if _, err := s.db.Exec(`CREATE VIRTUAL TABLE probe USING fts5(body, tokenize='unicode61 remove_diacritics 2')`); err != nil {
		t.Fatalf("this build has no FTS5, so no hand command can open hand.db: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name LIKE '%\_fts' ESCAPE '\' AND type = 'table'`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("fts tables = %d, %v; want 3", n, err)
	}
	var triggers int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'trigger' AND name LIKE '%\_fts\_%' ESCAPE '\'`).Scan(&triggers); err != nil || triggers != 9 {
		t.Fatalf("fts triggers = %d, %v; want 9", triggers, err)
	}
}

func TestAMovedFleetIsSearchedByItsNewPathAndNotTheOldOne(t *testing.T) {
	parent := t.TempDir()
	setUserHome(t, parent)
	old := filepath.Join(parent, "amberkiln")
	to := filepath.Join(parent, "birchferry")
	s := openAt(t, old)
	ctx := context.Background()
	h := &historyDB{t: s}
	task := activeTask(t, s)
	h.attempt = runningAttempt2(t, s, task)
	sup, err := s.AddSupervisor(ctx, SupervisorSpec{Harness: "claude", Argv: []string{"claude"}, Session: "sess1"})
	if err != nil {
		t.Fatal(err)
	}
	h.sup = sup
	at := "2026-09-26T00:00:00Z"
	h.input(t, at, "operator keeps "+old+"/memory")
	h.report(t, task.ID, at, "Delivered: "+old+"/reports/t1/x.md")
	h.reply(t, at, 0, "reply names "+old+"/inbox")
	for _, q := range []string{"amberkiln", "birchferry"} {
		want := 3
		if q == "birchferry" {
			want = 0
		}
		if res := h.search(t, HistoryQuery{Q: q}); res.Total != want {
			t.Fatalf("before the move %q found %d, want %d", q, res.Total, want)
		}
	}
	if _, err := s.MoveHome(ctx, old, to); err != nil {
		t.Fatal(err)
	}
	if res := h.search(t, HistoryQuery{Q: "amberkiln"}); res.Total != 0 {
		t.Fatalf("the old path still finds %+v", res.Hits)
	}
	if res := h.search(t, HistoryQuery{Q: "birchferry"}); res.Total != 3 || len(res.Hits) != 3 {
		t.Fatalf("the new path finds %d, want 3: %+v", res.Total, res.Hits)
	}
}

func runningAttempt2(t *testing.T, s *Store, task Task) Attempt {
	t.Helper()
	spec := claudeSpec
	spec.TaskID = task.ID
	a, err := s.AddAttempt(context.Background(), spec, "/w")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestTriggersKeepTheIndexOnInsertUpdateAndDelete(t *testing.T) {
	h := newHistory(t)
	at := "2026-11-18T10:00:00Z"
	i := h.input(t, at, "kiln schedule")
	y := h.reply(t, at, 0, "kiln offer")
	r := h.report(t, h.attempt.TaskID, at, "kiln done")
	if got := refs(h.search(t, HistoryQuery{Q: "kiln"})); !slices.Equal(got, []string{i, r, y}) {
		t.Fatalf("after insert: %v", got)
	}
	for _, table := range []string{"supervisor_input", "supervisor_reply", "report"} {
		h.exec(t, `UPDATE `+table+` SET body = 'ferry '||body WHERE id = 1`)
	}
	if got := refs(h.search(t, HistoryQuery{Q: "ferry"})); !slices.Equal(got, []string{i, r, y}) {
		t.Fatalf("after update the new word finds %v", got)
	}
	h.exec(t, `UPDATE supervisor_input SET body = 'gone' WHERE id = 1`)
	h.exec(t, `UPDATE supervisor_reply SET body = 'gone' WHERE id = 1`)
	h.exec(t, `UPDATE report SET body = 'gone' WHERE id = 1`)
	if res := h.search(t, HistoryQuery{Q: "kiln OR ferry"}); res.Total != 0 {
		t.Fatalf("after update the old words still find %+v", res.Hits)
	}
	if got := refs(h.search(t, HistoryQuery{Q: "gone"})); !slices.Equal(got, []string{i, r, y}) {
		t.Fatalf("after update: %v", got)
	}
	h.exec(t, `DELETE FROM supervisor_input WHERE id = 1`)
	h.exec(t, `DELETE FROM supervisor_reply WHERE id = 1`)
	h.exec(t, `DELETE FROM report WHERE id = 1`)
	if res := h.search(t, HistoryQuery{Q: "gone"}); res.Total != 0 {
		t.Fatalf("after delete: %+v", res.Hits)
	}
	h.input(t, at, "reused id")
	h.reply(t, at, 0, "reused id")
	h.report(t, h.attempt.TaskID, at, "reused id")
	if res := h.search(t, HistoryQuery{Q: "gone"}); res.Total != 0 {
		t.Fatalf("a row that reuses a deleted id answers to its words: %+v", res.Hits)
	}
	for _, f := range []string{"supervisor_input_fts", "supervisor_reply_fts", "report_fts"} {
		if _, err := h.t.db.Exec(fmt.Sprintf(`INSERT INTO %s(%s, rank) VALUES ('integrity-check', 1)`, f, f)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
}

func TestMigratingFrom25IndexesTheRowsAlreadyThere(t *testing.T) {
	s, path := openTest(t)
	h := &historyDB{t: s}
	h.attempt = runningAttempt(t, s)
	sup, err := s.AddSupervisor(context.Background(), SupervisorSpec{Harness: "claude", Argv: []string{"claude"}, Session: "sess1"})
	if err != nil {
		t.Fatal(err)
	}
	h.sup = sup
	for _, q := range append(slices.Clone(dropHistoryIndex), `PRAGMA user_version = 25`) {
		h.exec(t, q)
	}
	at := "2026-11-18T10:00:00Z"
	var want []string
	for i := range 12 {
		w := fmt.Sprintf("word%d", i)
		want = append(want, h.input(t, at, "operator "+w), h.reply(t, at, i, "reply "+w), h.report(t, h.attempt.TaskID, at, "report "+w))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	h.t = s
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != SchemaVersion {
		t.Fatalf("schema = %d, %v", v, err)
	}
	for _, f := range []string{"supervisor_input_fts", "supervisor_reply_fts", "report_fts"} {
		if _, err := s.db.Exec(fmt.Sprintf(`INSERT INTO %s(%s, rank) VALUES ('integrity-check', 1)`, f, f)); err != nil {
			t.Fatalf("%s does not match its base rows: %v", f, err)
		}
	}
	var got []string
	for i := range 12 {
		res := h.search(t, HistoryQuery{Q: fmt.Sprintf("word%d", i), Limit: 50})
		got = append(got, refs(res)...)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("the index finds %v, the tables hold %v", got, want)
	}
	h.input(t, at, "after the migration")
	if res := h.search(t, HistoryQuery{Q: "migration"}); res.Total != 1 {
		t.Fatalf("a row added after the migration is not indexed: %+v", res)
	}
}

func TestSearchFiltersByWhoSinceAndTask(t *testing.T) {
	h := newHistory(t)
	other, err := h.t.AddTask(context.Background(), "hand", "Other", "")
	if err != nil {
		t.Fatal(err)
	}
	i := h.input(t, "2026-11-10T10:00:00Z", "kiln operator")
	y := h.reply(t, "2026-11-12T10:00:00Z", 0, "kiln reply")
	r1 := h.report(t, h.attempt.TaskID, "2026-11-14T10:00:00Z", "kiln report")
	r2 := h.report(t, other.ID, "2026-11-16T10:00:00Z", "kiln report two")
	for _, c := range []struct {
		name string
		q    HistoryQuery
		want []string
	}{
		{"all", HistoryQuery{Q: "kiln"}, []string{i, r1, r2, y}},
		{"operator", HistoryQuery{Q: "kiln", Who: "operator"}, []string{i}},
		{"supervisor", HistoryQuery{Q: "kiln", Who: "supervisor"}, []string{y}},
		{"report", HistoryQuery{Q: "kiln", Who: "report"}, []string{r1, r2}},
		{"since a date", HistoryQuery{Q: "kiln", Since: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)}, []string{r1, r2, y}},
		{"since a second after", HistoryQuery{Q: "kiln", Since: time.Date(2026, 11, 12, 10, 0, 1, 0, time.UTC)}, []string{r1, r2}},
		{"since after all", HistoryQuery{Q: "kiln", Since: time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)}, []string{}},
		{"task of a report", HistoryQuery{Q: "kiln", TaskID: other.ID}, []string{r2}},
		{"task of the attempt", HistoryQuery{Q: "kiln", TaskID: h.attempt.TaskID, Who: "report"}, []string{r1}},
	} {
		if got := refs(h.search(t, c.q)); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTaskFilterMatchesTheRefTokenInMessagesAndReplies(t *testing.T) {
	h := newHistory(t)
	at := "2026-11-10T10:00:00Z"
	i := h.input(t, at, "kiln for t12 please")
	h.input(t, at, "kiln for t123 please")
	y := h.reply(t, at, 0, "kiln started on T12 now")
	h.reply(t, at, 1, "kiln elsewhere")
	got := refs(h.search(t, HistoryQuery{Q: "kiln", TaskID: 12}))
	if !slices.Equal(got, []string{i, y}) {
		t.Fatalf("got %v, want %v", got, []string{i, y})
	}
	if got := refs(h.search(t, HistoryQuery{Q: "kiln OR nothing", TaskID: 12})); !slices.Equal(got, []string{i, y}) {
		t.Fatalf("an OR query with a task filter found %v", got)
	}
}

func TestAQueryCannotEscapeTheTaskOrWhoFilter(t *testing.T) {
	h := newHistory(t)
	at := "2026-11-10T10:00:00Z"
	for i := range 6 {
		h.input(t, at, fmt.Sprintf("zed message %d", i))
	}
	h.reply(t, at, 0, "zed reply")
	h.report(t, h.attempt.TaskID, at, "zed report")
	want := h.input(t, at, "zed or zed for t5")
	for _, q := range []string{"zed) OR (zed", "zed) OR zed OR (zed", `zed" OR "zed`, "(zed) OR (zed"} {
		res := h.search(t, HistoryQuery{Q: q, Who: "operator", TaskID: 5})
		if got := refs(res); res.Total != 1 || !slices.Equal(got, []string{want}) {
			t.Errorf("%q with who operator and task t5 returned %v of %d", q, got, res.Total)
		}
		res = h.search(t, HistoryQuery{Q: q, TaskID: 5})
		if got := refs(res); res.Total != 1 || !slices.Equal(got, []string{want}) {
			t.Errorf("%q with task t5 and every source returned %v of %d", q, got, res.Total)
		}
		if res := h.search(t, HistoryQuery{Q: q, Who: "operator", Limit: 50}); res.Total != 1 || res.Hits[0].Who != "operator" {
			t.Errorf("%q with who operator returned %d", q, res.Total)
		}
	}
}

func TestSearchRanksThenNewestFirstAndCountsTheRest(t *testing.T) {
	h := newHistory(t)
	h.input(t, "2026-11-10T10:00:00Z", "kiln")
	h.input(t, "2026-11-11T10:00:00Z", "kiln")
	h.input(t, "2026-11-12T10:00:00Z", "kiln")
	res := h.search(t, HistoryQuery{Q: "kiln", Limit: 2})
	if res.Total != 3 || len(res.Hits) != 2 {
		t.Fatalf("total %d hits %d", res.Total, len(res.Hits))
	}
	if res.Hits[0].At != "2026-11-12T10:00:00Z" || res.Hits[1].At != "2026-11-11T10:00:00Z" {
		t.Fatalf("equal ranks are not newest first: %+v", res.Hits)
	}
	h.input(t, "2026-11-09T10:00:00Z", "kiln kiln kiln amber")
	h.input(t, "2026-11-09T11:00:00Z", strings.Repeat("padding ", 40)+"kiln")
	res = h.search(t, HistoryQuery{Q: "kiln", Limit: 1})
	if res.Hits[0].At != "2026-11-09T10:00:00Z" {
		t.Fatalf("the better match does not come first: %+v", res.Hits)
	}
}

func TestSearchHitsCarryTheirFields(t *testing.T) {
	h := newHistory(t)
	h.input(t, "2026-11-10T10:00:00Z", "operator amber")
	h.reply(t, "2026-11-11T10:00:00Z", 0, "reply amber")
	h.report(t, h.attempt.TaskID, "2026-11-12T10:00:00Z", "report amber")
	got := map[string]HistoryEntry{}
	for _, hit := range h.search(t, HistoryQuery{Q: "amber"}).Hits {
		got[hit.Who] = hit.HistoryEntry
	}
	want := map[string]HistoryEntry{
		"operator":   {Ref: "i1", Who: "operator", At: "2026-11-10T10:00:00Z", Text: "operator «amber»"},
		"supervisor": {Ref: "y1", Who: "supervisor", At: "2026-11-11T10:00:00Z", Supervisor: "s1", Text: "reply «amber»"},
		"report":     {Ref: "r1", Who: "report", At: "2026-11-12T10:00:00Z", Task: TaskRef(h.attempt.TaskID), Text: "report «amber»"},
	}
	for who, w := range want {
		if got[who] != w {
			t.Errorf("%s: got %+v, want %+v", who, got[who], w)
		}
	}
}

func TestBadSyntaxFallsBackToQuotedWords(t *testing.T) {
	h := newHistory(t)
	y := h.reply(t, "2026-11-10T10:00:00Z", 0, `the "amber kiln" and birch ferry`)
	z := h.reply(t, "2026-11-10T10:00:01Z", 0, `unterminated and nosuch amber`)
	for q, want := range map[string][]string{
		`"unterminated`: {z}, `amber AND`: {y, z}, `amber (kiln`: {y}, `nosuch:amber`: {z}, `birch ferry"`: {y}, `amber -`: {y, z}, `ferry ***`: {y},
	} {
		if got := refs(h.search(t, HistoryQuery{Q: q})); !slices.Equal(got, want) {
			t.Errorf("%q: got %v, want %v", q, got, want)
		}
	}
	if got := refs(h.search(t, HistoryQuery{Q: `amber AND "unterminated`, TaskID: 5})); len(got) != 0 {
		t.Errorf("fallback with a task filter: %v", got)
	}
	if got := refs(h.search(t, HistoryQuery{Q: `kiln OR "birch`})); len(got) != 0 {
		t.Errorf("fallback ANDs the words, got %v", got)
	}
}

func TestFTS5SyntaxStillWorksWhenItParses(t *testing.T) {
	h := newHistory(t)
	y1 := h.reply(t, "2026-11-10T10:00:00Z", 0, "amber kiln")
	y2 := h.reply(t, "2026-11-10T10:00:01Z", 0, "birch ferry")
	for q, want := range map[string][]string{
		`amber OR birch`:   {y1, y2},
		`"amber kiln"`:     {y1},
		`"kiln amber"`:     {},
		`amb*`:             {y1},
		`amber NOT kiln`:   {},
		`birch NOT kiln`:   {y2},
		`AMBER`:            {y1},
		`ámber`:            {y1},
		`kiln amber`:       {y1},
		`y1`:               {},
		`body: amber`:      {y1},
		`NEAR(amber kiln)`: {y1},
	} {
		if got := refs(h.search(t, HistoryQuery{Q: q})); !slices.Equal(got, want) {
			t.Errorf("%q: got %v, want %v", q, got, want)
		}
	}
}

func TestSearchRejectsWhatItCannotAnswer(t *testing.T) {
	h := newHistory(t)
	for name, q := range map[string]HistoryQuery{
		"empty":         {Q: "", Limit: 5},
		"blank":         {Q: " \t\n", Limit: 5},
		"long":          {Q: strings.Repeat("a", MaxHistoryQuery+1), Limit: 5},
		"who":           {Q: "a", Who: "worker", Limit: 5},
		"limit zero":    {Q: "a"},
		"limit big":     {Q: "a", Limit: MaxHistoryLimit + 1},
		"context":       {Q: "a", Limit: 5, Context: MaxHistoryNear + 1},
		"context neg":   {Q: "a", Limit: 5, Context: -1},
		"negative task": {Q: "a", Limit: 5, TaskID: -1},
	} {
		if _, err := h.t.SearchHistory(context.Background(), q); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestSearchWritesNothing(t *testing.T) {
	h := newHistory(t)
	h.input(t, "2026-11-10T10:00:00Z", "amber")
	before, _ := h.t.LastEventSeq(context.Background())
	h.search(t, HistoryQuery{Q: "amber", Context: 2})
	if _, err := h.t.HistoryEntry(context.Background(), "i1"); err != nil {
		t.Fatal(err)
	}
	if after, _ := h.t.LastEventSeq(context.Background()); after != before {
		t.Fatalf("a search wrote events: %d then %d", before, after)
	}
}

func shape(parts ...string) string { return strings.Join(parts, "") }

func TestSnippetsAndEntriesAreRedactedWhateverTheWindow(t *testing.T) {
	h := newHistory(t)
	secret := shape("gh", "p_", strings.Repeat("Ab3dE", 8))
	tail := secret[len("ghp_"):]
	for pad := range 40 {
		body := "start " + shape("token ", secret) + strings.Repeat(" filler", pad) + " omega"
		h.input(t, "2026-11-10T10:00:00Z", body)
	}
	res := h.search(t, HistoryQuery{Q: "omega", Limit: 50})
	if len(res.Hits) != 40 {
		t.Fatalf("hits = %d", len(res.Hits))
	}
	for _, hit := range res.Hits {
		if strings.Contains(hit.Text, tail) || strings.Contains(hit.Text, secret) {
			t.Fatalf("%s leaks a secret: %q", hit.Ref, hit.Text)
		}
		if !strings.Contains(hit.Text, "omega") {
			t.Fatalf("%s lost its match: %q", hit.Ref, hit.Text)
		}
		e, err := h.t.HistoryEntry(context.Background(), hit.Ref)
		if err != nil || strings.Contains(e.Text, tail) || !strings.Contains(e.Text, "[redacted:github-token]") {
			t.Fatalf("%s entry = %q, %v", hit.Ref, e.Text, err)
		}
	}
	y := h.reply(t, "2026-11-10T10:00:00Z", 0, "next "+secret+" omega2")
	res = h.search(t, HistoryQuery{Q: "omega2", Context: 1})
	if len(res.Hits) != 1 || res.Hits[0].Ref != y {
		t.Fatalf("hits = %+v", res.Hits)
	}
	for _, c := range res.Hits[0].Context {
		if strings.Contains(c.Text, tail) {
			t.Fatalf("context leaks: %q", c.Text)
		}
	}
}

func TestARedactedWindowStartsOnARuneBoundary(t *testing.T) {
	h := newHistory(t)
	secret := shape("gh", "p_", strings.Repeat("Ab3dE", 8))
	for lead := range 4 {
		h.input(t, "2026-11-10T10:00:00Z", strings.Repeat("x", lead)+strings.Repeat("é", 100)+" "+secret+" omega")
	}
	res := h.search(t, HistoryQuery{Q: "omega", Limit: 50})
	if len(res.Hits) != 4 {
		t.Fatalf("hits = %d", len(res.Hits))
	}
	for _, hit := range res.Hits {
		if !utf8.ValidString(hit.Text) || strings.ContainsRune(hit.Text, '\uFFFD') || !strings.Contains(hit.Text, "[redacted:github-token]") {
			t.Fatalf("%s window = %q", hit.Ref, hit.Text)
		}
	}
}

func TestSnippetsAreOneLineAndBounded(t *testing.T) {
	h := newHistory(t)
	h.input(t, "2026-11-10T10:00:00Z", "amber\n\n\tkiln\x01\x1b[31m "+strings.Repeat("x", 5000)+" birch")
	h.input(t, "2026-11-10T11:00:00Z", strings.Repeat("é", 100)+" amber "+strings.Repeat("ü", 400))
	for _, hit := range h.search(t, HistoryQuery{Q: "amber"}).Hits {
		if len(hit.Text) > historySnippet || !utf8.ValidString(hit.Text) || strings.ContainsAny(hit.Text, "\n\t\x01\x1b") {
			t.Fatalf("snippet = %q (%d bytes)", hit.Text, len(hit.Text))
		}
	}
}

func TestContextIsTheNeighboursInTheMergedConversation(t *testing.T) {
	h := newHistory(t)
	i1 := h.input(t, "2026-11-10T10:00:00Z", "one")
	y1 := h.reply(t, "2026-11-10T10:01:00Z", 0, "two")
	i2 := h.input(t, "2026-11-10T10:02:00Z", "three")
	y2 := h.reply(t, "2026-11-10T10:03:00Z", 0, "four amber")
	y3 := h.reply(t, "2026-11-10T10:03:00Z", 1, "five")
	y4 := h.reply(t, "2026-11-10T10:03:00Z", 2, "six")
	i3 := h.input(t, "2026-11-10T10:04:00Z", "seven")
	ctxRefs := func(c []HistoryEntry) []string {
		var out []string
		for _, e := range c {
			out = append(out, e.Ref)
		}
		return out
	}
	hit := h.search(t, HistoryQuery{Q: "amber", Context: 2}).Hits[0]
	if got, want := ctxRefs(hit.Context), []string{y1, i2, y3, y4}; !slices.Equal(got, want) {
		t.Fatalf("context = %v, want %v", got, want)
	}
	if hit.Context[0].Who != "supervisor" || hit.Context[0].Supervisor != "s1" || hit.Context[0].Text != "two" || hit.Context[1].Who != "operator" {
		t.Fatalf("context fields = %+v", hit.Context)
	}
	if got := ctxRefs(h.search(t, HistoryQuery{Q: "amber", Context: 5}).Hits[0].Context); !slices.Equal(got, []string{i1, y1, i2, y3, y4, i3}) {
		t.Fatalf("wide context = %v", got)
	}
	if hit := h.search(t, HistoryQuery{Q: "one"}).Hits[0]; hit.Context != nil {
		t.Fatalf("context 0 returned %+v", hit.Context)
	}
	first := h.search(t, HistoryQuery{Q: "one", Context: 1}).Hits[0]
	if got := ctxRefs(first.Context); !slices.Equal(got, []string{y1}) {
		t.Fatalf("context at the start = %v", got)
	}
	_ = y2
}

func TestContextIsIgnoredForAReport(t *testing.T) {
	h := newHistory(t)
	h.input(t, "2026-11-10T10:00:00Z", "one")
	h.report(t, h.attempt.TaskID, "2026-11-10T10:01:00Z", "amber report")
	h.input(t, "2026-11-10T10:02:00Z", "two")
	if hit := h.search(t, HistoryQuery{Q: "amber", Context: 2}).Hits[0]; hit.Context != nil {
		t.Fatalf("a report carries context %+v", hit.Context)
	}
}

func TestHistoryEntryReadsOneRowWhole(t *testing.T) {
	h := newHistory(t)
	i := h.input(t, "2026-11-10T10:00:00Z", "line one\nline two")
	y := h.reply(t, "2026-11-10T10:01:00Z", 0, "reply")
	r := h.report(t, h.attempt.TaskID, "2026-11-10T10:02:00Z", "report")
	ctx := context.Background()
	for ref, want := range map[string]HistoryEntry{
		i: {Ref: i, Who: "operator", At: "2026-11-10T10:00:00Z", Text: "line one\nline two"},
		y: {Ref: y, Who: "supervisor", At: "2026-11-10T10:01:00Z", Supervisor: "s1", Text: "reply"},
		r: {Ref: r, Who: "report", At: "2026-11-10T10:02:00Z", Task: TaskRef(h.attempt.TaskID), Text: "report"},
	} {
		if got, err := h.t.HistoryEntry(ctx, ref); err != nil || got != want {
			t.Errorf("%s = %+v, %v; want %+v", ref, got, err, want)
		}
	}
	for _, ref := range []string{"i99", "r99", "y99", "t1", "x", "", "i0", "i-1", "y01", "i99999999999999999999"} {
		if _, err := h.t.HistoryEntry(ctx, ref); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q err = %v, want ErrNotFound", ref, err)
		}
	}
}

func TestHistoryEntryIsCappedAtARuneBoundary(t *testing.T) {
	h := newHistory(t)
	i := h.reply(t, "2026-11-10T10:00:00Z", 0, strings.Repeat("€", 10000))
	r := h.report(t, h.attempt.TaskID, "2026-11-10T10:02:00Z", strings.Repeat("€", 20000))
	small, err := h.t.HistoryEntry(context.Background(), i)
	if err != nil || !small.Truncated || len(small.Text) > MaxHistoryBody || !utf8.ValidString(small.Text) || small.Omitted+len(small.Text) != 30000 {
		t.Fatalf("input: truncated %v, %d bytes, omitted %d, %v", small.Truncated, len(small.Text), small.Omitted, err)
	}
	big, err := h.t.HistoryEntry(context.Background(), r)
	if err != nil || !big.Truncated || len(big.Text) > MaxHistoryBody || !utf8.ValidString(big.Text) || big.Omitted+len(big.Text) != 60000 {
		t.Fatalf("report: truncated %v, %d bytes, omitted %d, %v", big.Truncated, len(big.Text), big.Omitted, err)
	}
	multi := h.input(t, "2026-11-10T10:04:00Z", strings.Repeat("€", 6000))
	if e, err := h.t.HistoryEntry(context.Background(), multi); err != nil || !e.Truncated || !utf8.ValidString(e.Text) || e.Omitted+len(e.Text) != 18000 || len(e.Text) > MaxHistoryBody {
		t.Fatalf("a multibyte operator message: truncated %v, %d bytes, omitted %d, %v", e.Truncated, len(e.Text), e.Omitted, err)
	}
	ok := h.input(t, "2026-11-10T10:03:00Z", strings.Repeat("x", MaxHistoryBody))
	if e, _ := h.t.HistoryEntry(context.Background(), ok); e.Truncated || e.Omitted != 0 || len(e.Text) != MaxHistoryBody {
		t.Fatalf("a body of exactly the cap was cut: %v %d", e.Truncated, len(e.Text))
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{
		"":           {},
		"7d":         now.AddDate(0, 0, -7),
		"36h":        now.Add(-36 * time.Hour),
		"1h":         now.Add(-time.Hour),
		"2026-11-01": time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
	} {
		if got, err := ParseSince(in, now); err != nil || !got.Equal(want) {
			t.Errorf("%q = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"d", "0d", "-1d", "7", "7w", "1.5d", "2026-13-01", "yesterday", "7d ", "99999999d"} {
		if _, err := ParseSince(bad, now); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q err = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestSearchSeesAReadOnlyCopy(t *testing.T) {
	h := newHistory(t)
	h.input(t, "2026-11-10T10:00:00Z", "amber")
	path := filepath.Join(t.TempDir(), "copy.db")
	if err := h.t.Backup(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	ro, err := OpenRead(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	res, err := ro.SearchHistory(context.Background(), HistoryQuery{Q: "amber", Limit: 5})
	if err != nil || res.Total != 1 {
		t.Fatalf("read-only search = %+v, %v", res, err)
	}
}

var _ = sql.ErrNoRows
