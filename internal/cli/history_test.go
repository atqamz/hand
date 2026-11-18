package cli_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

type historySeed struct {
	t  *testing.T
	db *sql.DB
}

func newHistorySeed(t *testing.T) (*harness, *historySeed) {
	t.Helper()
	h := newHarness(t)
	h.ok("init")
	db, err := sql.Open("sqlite", "file:"+filepath.Join(h.home, "hand.db")+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return h, &historySeed{t, db}
}

func (s *historySeed) exec(q string, args ...any) {
	s.t.Helper()
	if _, err := s.db.Exec(q, args...); err != nil {
		s.t.Fatalf("%s: %v", q, err)
	}
}

func (s *historySeed) input(id int, at, body string) {
	s.exec(`INSERT INTO supervisor_input(id, body, created_at) VALUES (?, ?, ?)`, id, body, at)
}

func (s *historySeed) reply(id, sup int, at, body string) {
	s.exec(`INSERT INTO supervisor_reply(id, supervisor_id, session, at, n, body) VALUES (?, ?, 'sess', ?, ?, ?)`, id, sup, at, id, body)
}

func (s *historySeed) report(id, task int, at, body string) {
	s.exec(`INSERT INTO report(id, attempt_id, task_id, status, body, created_at) VALUES (?, 1, ?, 'done', ?, ?)`, id, task, body, at)
}

func TestHistorySearchPrintsOneRowPerHit(t *testing.T) {
	h, s := newHistorySeed(t)
	s.input(1, "2026-11-10T10:00:00Z", "open the amber kiln")
	s.reply(2, 74, "2026-11-11T10:00:00Z", "1. Amber Kiln\n2. Birch Ferry")
	s.report(3, 12, "2026-11-12T10:00:00Z", "amber report")
	out := h.ok("history", "search", "amber")
	if !strings.HasPrefix(out, "total: 3\nhits[3]{ref,at,who,task,supervisor,text}:\n  r3,") {
		t.Fatalf("out = %q", out)
	}
	for _, row := range []string{
		`i1,"2026-11-10T10:00:00Z",operator,"","",open the «amber» kiln`,
		`y2,"2026-11-11T10:00:00Z",supervisor,"",s74,1. «Amber» Kiln 2. Birch Ferry`,
		`r3,"2026-11-12T10:00:00Z",report,t12,"",«amber» report`,
	} {
		if !strings.Contains(out, "\n  "+row+"\n") {
			t.Errorf("no row %q in %q", row, out)
		}
	}
	if !strings.Contains(out, "help[1]:\n  - `hand history search --full REF` prints one entry whole\n") {
		t.Fatalf("help: %q", out)
	}
}

func TestHistorySearchTakesFlagsAfterTheQueryAndJoinsWords(t *testing.T) {
	h, s := newHistorySeed(t)
	s.input(1, "2026-11-10T10:00:00Z", "amber kiln")
	s.input(2, "2026-11-10T11:00:00Z", "amber only")
	s.reply(3, 1, "2026-11-10T12:00:00Z", "amber kiln reply")
	out := h.ok("history", "search", "amber", "kiln", "--who", "operator", "--limit", "1")
	if !strings.Contains(out, "total: 1\n") || !strings.Contains(out, "\n  i1,") || strings.Contains(out, "y3") {
		t.Fatalf("out = %q", out)
	}
	out = h.ok("history", "search", "--who", "supervisor", "amber kiln")
	if !strings.Contains(out, "total: 1\n") || !strings.Contains(out, "\n  y3,") {
		t.Fatalf("out = %q", out)
	}
}

func TestHistorySearchFiltersBySinceAndTask(t *testing.T) {
	h, s := newHistorySeed(t)
	s.input(1, "2026-09-20T10:00:00Z", "amber old")
	s.input(2, "2026-09-25T10:00:00Z", "amber new")
	s.report(3, 7, "2026-09-25T11:00:00Z", "amber seven")
	s.report(4, 8, "2026-09-25T12:00:00Z", "amber eight")
	if out := h.ok("history", "search", "amber", "--since", "3d"); !strings.Contains(out, "total: 3\n") || strings.Contains(out, "i1,") {
		t.Fatalf("since 3d = %q", out)
	}
	if out := h.ok("history", "search", "amber", "--since", "2026-09-26"); !strings.Contains(out, "hits[0]") {
		t.Fatalf("since a date = %q", out)
	}
	if out := h.ok("history", "search", "amber", "--task", "t7"); !strings.Contains(out, "total: 1\n") || !strings.Contains(out, "r3,") {
		t.Fatalf("task = %q", out)
	}
}

func TestHistorySearchWithNoHitExitsZero(t *testing.T) {
	h, s := newHistorySeed(t)
	s.input(1, "2026-09-20T10:00:00Z", "amber")
	out := h.ok("history", "search", "nothing")
	if !strings.Contains(out, "total: 0\nhits[0]{ref,at,who,task,supervisor,text}:\n") || !strings.Contains(out, "--since, --who or --task") {
		t.Fatalf("out = %q", out)
	}
}

func TestHistorySearchRefusesWhatItCannotRun(t *testing.T) {
	h, _ := newHistorySeed(t)
	for name, args := range map[string][]string{
		"no query":      {"history", "search"},
		"empty query":   {"history", "search", ""},
		"blank query":   {"history", "search", "   "},
		"limit":         {"history", "search", "a", "--limit", "0"},
		"limit big":     {"history", "search", "a", "--limit", "51"},
		"context":       {"history", "search", "a", "--context", "6"},
		"who":           {"history", "search", "a", "--who", "worker"},
		"since":         {"history", "search", "a", "--since", "soon"},
		"task":          {"history", "search", "a", "--task", "x"},
		"flag":          {"history", "search", "a", "--nope"},
		"full and text": {"history", "search", "a", "--full", "i1"},
		"full ref":      {"history", "search", "--full", "t1"},
		"no subcommand": {"history"},
		"subcommand":    {"history", "find"},
	} {
		if out, errOut, code := h.run(args...); code != 2 || out != "" || errOut == "" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit 2", name, code, out, errOut)
		}
	}
	if _, errOut, code := h.run("history", "search", "--full", "i99"); code != 3 || !strings.Contains(errOut, "no entry i99") {
		t.Errorf("unknown ref: exit %d, stderr %q", code, errOut)
	}
}

func TestHistorySearchRedactsWhatItPrints(t *testing.T) {
	h, s := newHistorySeed(t)
	secret := strings.Join([]string{"gh", "p_", strings.Repeat("Zq9xK", 8)}, "")
	s.input(1, "2026-09-20T10:00:00Z", "use "+secret+" for amber")
	s.reply(2, 1, "2026-09-20T11:00:00Z", "amber and "+secret)
	s.report(3, 1, "2026-09-20T12:00:00Z", "amber with "+secret)
	out := h.ok("history", "search", "amber", "--context", "1")
	if strings.Contains(out, secret) || strings.Contains(out, secret[4:]) || !strings.Contains(out, "[redacted:github-token]") {
		t.Fatalf("search = %q", out)
	}
	for _, ref := range []string{"i1", "y2", "r3"} {
		if out := h.ok("history", "search", "--full", ref); strings.Contains(out, secret[4:]) || !strings.Contains(out, "[redacted:github-token]") {
			t.Fatalf("full %s = %q", ref, out)
		}
	}
}

func TestHistorySearchDefaultOutputStaysUnder4KB(t *testing.T) {
	h, s := newHistorySeed(t)
	quotes := "amber " + strings.Repeat(`"`, 2000) + " kiln"
	emoji := "amber " + strings.Repeat("😀", 400) + " kiln"
	long := "2026-11-18T12:14:55.123456789Z"
	s.input(9999991, long, quotes)
	s.reply(9999992, 99999, long, quotes)
	s.report(9999993, 99999, long, quotes)
	s.reply(9999994, 99999, long, quotes)
	s.input(9999995, long, quotes)
	out := h.ok("history", "search", "amber")
	if len(out) > 4096 || len(out) < 3300 || strings.Count(out, "\n  y") != 2 {
		t.Fatalf("five maximal hits print %d bytes, want 3300 to 4096", len(out))
	}
	s.exec(`DELETE FROM supervisor_input`)
	s.exec(`DELETE FROM supervisor_reply`)
	s.exec(`DELETE FROM report`)
	for i := range 5 {
		s.input(9999990+i, long, emoji)
	}
	if out := h.ok("history", "search", "amber"); len(out) > 4096 {
		t.Fatalf("five emoji hits print %d bytes", len(out))
	}
}

func TestHistorySearchStopsAtTheCeilingAndCountsTheRest(t *testing.T) {
	h, s := newHistorySeed(t)
	for i := 1; i <= 60; i++ {
		s.input(i, fmt.Sprintf("2026-11-10T10:%02d:00Z", i%60), "amber "+strings.Repeat(`"`, 1500)+" kiln")
	}
	out := h.ok("history", "search", "amber", "--limit", "50", "--context", "5")
	if len(out) > 16<<10 {
		t.Fatalf("output is %d bytes", len(out))
	}
	if !strings.Contains(out, "total: 60\n") || !strings.Contains(out, " more; narrow with --since, --who or --task") {
		t.Fatalf("no more line: %q", out[len(out)-400:])
	}
	var shown, more int
	if _, err := fmt.Sscanf(out[strings.Index(out, "hits["):], "hits[%d]", &shown); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Sscanf(out[strings.Index(out, "  - ")+4:], "%d more", &more); err != nil || shown+more != 60 || shown >= 50 {
		t.Fatalf("shown %d, more %d, %v", shown, more, err)
	}
}

func TestHistorySearchContextRows(t *testing.T) {
	h, s := newHistorySeed(t)
	s.input(1, "2026-11-10T10:00:00Z", "before it")
	s.reply(2, 3, "2026-11-10T10:01:00Z", "the amber one")
	s.input(3, "2026-11-10T10:02:00Z", "after it")
	out := h.ok("history", "search", "amber", "--context", "1")
	for _, row := range []string{
		"context[2]{hit,ref,at,who,text}:",
		`  y2,i1,"2026-11-10T10:00:00Z",operator,before it`,
		`  y2,i3,"2026-11-10T10:02:00Z",operator,after it`,
	} {
		if !strings.Contains(out, row+"\n") {
			t.Errorf("no %q in %q", row, out)
		}
	}
}

func TestHistorySearchFullPrintsOneEntryWhole(t *testing.T) {
	h, s := newHistorySeed(t)
	s.report(1, 12, "2026-11-10T10:00:00Z", "first line\nsecond line")
	out := h.ok("history", "search", "--full", "r1")
	want := "ref: r1\nwho: report\nat: \"2026-11-10T10:00:00Z\"\ntask: t12\ntext: |\n  first line\n  second line\n"
	if out != want {
		t.Fatalf("out = %q", out)
	}
	s.reply(2, 5, "2026-11-10T11:00:00Z", strings.Repeat("0123456789abcdef", 2000))
	s.reply(3, 5, "2026-11-10T11:01:00Z", strings.Repeat("x\n", 12000))
	s.reply(4, 5, "2026-11-10T11:02:00Z", strings.Repeat("y", 16384))
	s.reply(5, 5, "2026-11-10T11:03:00Z", strings.Repeat("€", 9000))
	notice := regexp.MustCompile(`\n  … (\d+) bytes omitted at 16 KiB\n$`)
	for ref, c := range map[string]struct{ total, perLine int }{"y2": {32000, 0}, "y3": {24000, 2}, "y4": {16384, 0}, "y5": {27000, 0}} {
		out := h.ok("history", "search", "--full", ref)
		m := notice.FindStringSubmatch(out)
		if len(out) > 16<<10 || m == nil || !strings.Contains(out, "supervisor: s5\n") || strings.Contains(out, "task:") {
			t.Fatalf("%s prints %d bytes, notice %v: %.100q", ref, len(out), m, out[len(out)-100:])
		}
		omitted, _ := strconv.Atoi(m[1])
		body := strings.TrimPrefix(notice.ReplaceAllString(out[strings.Index(out, "text: |\n")+len("text: |\n"):], ""), "  ")
		kept := len(body)
		if c.perLine > 0 {
			kept = strings.Count(body, "x\n") * c.perLine
		}
		if omitted+kept != c.total {
			t.Fatalf("%s: kept %d and omitted %d is not %d", ref, kept, omitted, c.total)
		}
		if len(out) < 16<<10-2000 {
			t.Fatalf("%s prints only %d bytes", ref, len(out))
		}
	}
	s.input(6, "2026-11-10T11:04:00Z", strings.Repeat("€", 6000))
	out = h.ok("history", "search", "--full", "i6")
	m := notice.FindStringSubmatch(out)
	if m == nil || len(out) > 16<<10 || !utf8.ValidString(out) || strings.ContainsRune(out, '\uFFFD') {
		t.Fatalf("i6 prints %d bytes, notice %v, valid %v", len(out), m, utf8.ValidString(out))
	}
	omitted, _ := strconv.Atoi(m[1])
	body := strings.TrimPrefix(notice.ReplaceAllString(out[strings.Index(out, "text: |\n")+len("text: |\n"):], ""), "  ")
	if omitted+len(body) != 18000 {
		t.Fatalf("i6: kept %d and omitted %d is not 18000", len(body), omitted)
	}
	if out := h.ok("history", "search", "--full", "r1"); strings.Contains(out, "omitted") {
		t.Fatalf("a short entry reports an omission: %q", out)
	}
}

func TestHistorySearchLeavesOrientAlone(t *testing.T) {
	h, s := newHistorySeed(t)
	s.input(1, "2026-11-10T10:00:00Z", "amber")
	before := h.ok("orient")
	h.ok("history", "search", "amber", "--context", "2")
	h.ok("history", "search", "--full", "i1")
	h.ok("history", "search", "nothing")
	if after := h.ok("orient"); after != before {
		t.Fatalf("orient changed:\n%s\n%s", before, after)
	}
}

func TestHistorySearchShowsFiveHitsByDefaultAndSaysHowManyMore(t *testing.T) {
	h, s := newHistorySeed(t)
	for i := 1; i <= 7; i++ {
		s.input(i, fmt.Sprintf("2026-11-10T10:0%d:00Z", i), "amber")
	}
	out := h.ok("history", "search", "amber")
	if !strings.Contains(out, "total: 7\nhits[5]{") || !strings.Contains(out, "  - 2 more; narrow with --since, --who or --task") {
		t.Fatalf("out = %q", out)
	}
	if out := h.ok("history", "search", "amber", "--limit", "7"); !strings.Contains(out, "hits[7]{") || strings.Contains(out, " more;") || strings.Contains(out, "context[") {
		t.Fatalf("out = %q", out)
	}
}

func TestHistorySearchNamesTheCommandItWasRunAs(t *testing.T) {
	h, s := newHistorySeed(t)
	s.input(1, "2026-11-10T10:00:00Z", "amber")
	h.name = "hand-next"
	if out := h.ok("history", "search", "amber"); !strings.Contains(out, "`hand-next history search --full REF`") {
		t.Fatalf("out = %q", out)
	}
}
