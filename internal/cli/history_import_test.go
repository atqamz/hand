package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fakebin"
)

type importRig struct {
	h *harness
	*historySeed
	claude, codex string
}

func newImportRig(t *testing.T) *importRig {
	t.Helper()
	h, s := newHistorySeed(t)
	g := &importRig{h: h, historySeed: s, claude: t.TempDir(), codex: t.TempDir()}
	h.vars["CLAUDE_CONFIG_DIR"] = g.claude
	h.vars["CODEX_HOME"] = g.codex
	return g
}

func (g *importRig) sup(id int, harness, session, born, ended string) {
	g.exec(`INSERT INTO supervisor(id, harness, model, effort, argv, session, status, created_at, ended_at) VALUES (?, ?, 'm', 'low', '[]', ?, 'stopped', ?, ?)`, id, harness, session, born, ended)
}

func importUser(at, text string) string {
	return fmt.Sprintf(`{"type":"user","timestamp":%q,"message":{"role":"user","content":%q}}`, at, text)
}

func importReply(at, text string) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":%q}]}}`, at, text)
}

func (g *importRig) claudeFile(session string, lines ...string) {
	g.t.Helper()
	dir := filepath.Join(g.claude, "projects", "p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		g.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, session+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		g.t.Fatal(err)
	}
}

func (g *importRig) codexFile(session string, replies ...string) {
	g.t.Helper()
	dir := filepath.Join(g.codex, "sessions", "2026", "11", "10")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		g.t.Fatal(err)
	}
	lines := []string{fmt.Sprintf(`{"timestamp":"2026-11-10T13:00:00Z","type":"session_meta","payload":{"id":%q,"cwd":"/f"}}`, session)}
	for i, text := range replies {
		at := fmt.Sprintf("2026-11-10T13:%02d:00Z", i+1)
		lines = append(lines,
			fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"go"}]}}`, at),
			fmt.Sprintf(`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}}`, at, text))
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout-"+session+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		g.t.Fatal(err)
	}
}

func (g *importRig) count(q string, args ...any) int {
	g.t.Helper()
	var n int
	if err := g.db.QueryRow(q, args...).Scan(&n); err != nil {
		g.t.Fatal(err)
	}
	return n
}

func (g *importRig) stored() []string {
	g.t.Helper()
	rows, err := g.db.Query(`SELECT supervisor_id, session, at, n, body FROM supervisor_reply ORDER BY id`)
	if err != nil {
		g.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sup, n int
		var session, at, body string
		if err := rows.Scan(&sup, &session, &at, &n, &body); err != nil {
			g.t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("s%d %s %s %d %s", sup, session, at, n, body))
	}
	return out
}

func (g *importRig) fleet() {
	g.sup(1, "claude", "sc", "2026-11-10T09:00:00Z", "2026-11-10T12:00:00Z")
	g.sup(2, "claude", "sc", "2026-11-10T12:00:01Z", "2026-11-10T12:59:59Z")
	g.sup(3, "codex", "sx", "2026-11-10T13:00:00Z", "2026-11-10T14:00:00Z")
	g.sup(4, "claude", "", "2026-11-10T15:00:00Z", "")
	g.sup(5, "claude", "gone", "2026-11-10T16:00:00Z", "")
	g.sup(6, "claude", "junk", "2026-11-10T17:00:00Z", "")
	g.sup(7, "opencode", "ses_1", "2026-11-10T18:00:00Z", "")
	g.claudeFile("sc",
		importUser("2026-11-10T10:00:00.000Z", "status?"),
		importReply("2026-11-10T10:00:05.000Z", "first amber reply"),
		importReply("2026-11-10T10:00:05.000Z", "second amber reply"),
		importUser("2026-11-10T12:00:10.000Z", "and now?"),
		importReply("2026-11-10T12:00:30.000Z", "resumed birch reply"),
	)
	g.codexFile("sx", "codex line one\ncodex line two")
	g.claudeFile("junk", "not json", "still not json")
}

const importFleetRows = "sessions[6]{ref,harness,session,entries,inserted,status}:\n" +
	"  s1,claude,sc,3,3,ok\n" +
	"  s3,codex,sx,1,1,ok\n" +
	"  s4,claude,\"\",0,0,no session recorded\n" +
	"  s5,claude,gone,0,0,no transcript\n" +
	"  s6,claude,junk,0,0,unreadable\n" +
	"  s7,opencode,ses_1,0,0,harness missing\n"

func TestHistoryImportStoresEverySessionAndNamesEachStatus(t *testing.T) {
	t.Parallel()
	g := newImportRig(t)
	g.fleet()
	out := g.h.ok("history", "import")
	if !strings.Contains(out, "total: 6 sessions; 4 entries; 4 inserted\n"+importFleetRows) {
		t.Fatalf("out = %q", out)
	}
	want := []string{
		"s1 sc 2026-11-10T10:00:05.000Z 0 first amber reply",
		"s1 sc 2026-11-10T10:00:05.000Z 1 second amber reply",
		"s2 sc 2026-11-10T12:00:30.000Z 0 resumed birch reply",
		"s3 sx 2026-11-10T13:01:00Z 0 codex line one\ncodex line two",
	}
	if got := g.stored(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows = %q, want %q", got, want)
	}
}

func TestHistoryImportASecondRunInsertsNothingAndWritesNoEvent(t *testing.T) {
	t.Parallel()
	g := newImportRig(t)
	g.fleet()
	g.h.ok("history", "import")
	events := g.count(`SELECT count(*) FROM event`)
	rows := g.stored()
	out := g.h.ok("history", "import")
	if !strings.Contains(out, "total: 6 sessions; 4 entries; 0 inserted\n") || !strings.Contains(out, "\n  s1,claude,sc,3,0,ok\n") {
		t.Fatalf("second run = %q", out)
	}
	if got := g.count(`SELECT count(*) FROM event`); got != events || strings.Join(g.stored(), "|") != strings.Join(rows, "|") {
		t.Fatalf("second run wrote: events %d to %d", events, got)
	}
}

func TestHistoryImportBesideALivePassKeepsItsRowsAndAddsOnlyTheRest(t *testing.T) {
	t.Parallel()
	g := newImportRig(t)
	g.fleet()
	g.exec(`INSERT INTO supervisor_reply(supervisor_id, session, at, n, body) VALUES (2, 'sc', '2026-11-10T10:00:05.000Z', 0, 'stored live')`)
	out := g.h.ok("history", "import", "--session", "sc")
	if !strings.Contains(out, "\n  s1,claude,sc,3,2,ok\n") {
		t.Fatalf("out = %q", out)
	}
	if got := g.stored()[0]; got != "s2 sc 2026-11-10T10:00:05.000Z 0 stored live" {
		t.Fatalf("live row = %q", got)
	}
	if n := g.count(`SELECT count(*) FROM supervisor_reply WHERE session = 'sc'`); n != 3 {
		t.Fatalf("%d rows", n)
	}
}

func TestHistoryImportDryRunWritesNothingAndReportsWhatARunWouldInsert(t *testing.T) {
	t.Parallel()
	g := newImportRig(t)
	g.fleet()
	seq := g.count(`SELECT max(seq) FROM event`)
	out := g.h.ok("history", "import", "--dry-run")
	if !strings.HasPrefix(out, "dry_run: true\ntotal: 6 sessions; 4 entries; 4 would be inserted\n"+importFleetRows) {
		t.Fatalf("out = %q", out)
	}
	if n := g.count(`SELECT count(*) FROM supervisor_reply`); n != 0 {
		t.Fatalf("dry run stored %d rows", n)
	}
	if got := g.count(`SELECT max(seq) FROM event`); got != seq {
		t.Fatalf("dry run wrote events: %d to %d", seq, got)
	}
	g.h.ok("history", "import", "--session", "sx")
	out = g.h.ok("history", "import", "--dry-run")
	if !strings.Contains(out, "4 entries; 3 would be inserted\n") || !strings.Contains(out, "\n  s3,codex,sx,1,0,ok\n") {
		t.Fatalf("after a partial run = %q", out)
	}
}

func TestHistoryImportSessionFilterTakesOnlyTheNamedSessions(t *testing.T) {
	t.Parallel()
	g := newImportRig(t)
	g.fleet()
	out := g.h.ok("history", "import", "--session", "sx", "--session", "gone")
	if !strings.Contains(out, "total: 2 sessions; 1 entries; 1 inserted\n") || strings.Contains(out, "s1,") || strings.Contains(out, "s4,") {
		t.Fatalf("out = %q", out)
	}
	if got := g.stored(); len(got) != 1 || !strings.HasPrefix(got[0], "s3 sx ") {
		t.Fatalf("stored = %q", got)
	}
	_, errOut, code := g.h.run("history", "import", "--session", "sc", "--session", "nope")
	if code != 3 || !strings.Contains(errOut, "no supervisor recorded session nope") || len(g.stored()) != 1 {
		t.Fatalf("unknown session: exit %d %q, %d rows", code, errOut, len(g.stored()))
	}
	if _, _, code := g.h.run("history", "import", "--session", ""); code != 2 {
		t.Fatalf("empty session: exit %d", code)
	}
}

func TestHistoryImportWritesLongSessionsInChunksOf500(t *testing.T) {
	t.Parallel()
	g := newImportRig(t)
	g.sup(1, "claude", "big", "2026-11-10T09:00:00Z", "")
	var lines []string
	for i := 0; i < 1100; i++ {
		at := fmt.Sprintf("2026-11-10T10:%02d:%02d.000Z", i/60, i%60)
		lines = append(lines, importUser(at, "go"), importReply(at, fmt.Sprintf("reply %d", i)))
	}
	g.claudeFile("big", lines...)
	out := g.h.ok("history", "import")
	if !strings.Contains(out, "\n  s1,claude,big,1100,1100,ok\n") {
		t.Fatalf("out = %q", out)
	}
	r, err := g.db.Query(`SELECT detail FROM event WHERE kind = 'supervisor.replied' ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var details []string
	for r.Next() {
		var d string
		if err := r.Scan(&d); err != nil {
			t.Fatal(err)
		}
		details = append(details, d)
	}
	if got := strings.Join(details, "|"); got != "s1: y1..y500 (500)|s1: y501..y1000 (500)|s1: y1001..y1100 (100)" {
		t.Fatalf("events = %q", got)
	}
}

func TestHistoryImportRepliesAppearInHistorySearch(t *testing.T) {
	t.Parallel()
	g := newImportRig(t)
	g.fleet()
	g.h.ok("history", "import")
	out := g.h.ok("history", "search", "birch", "--who", "supervisor")
	if !strings.Contains(out, "total: 1\n") || !strings.Contains(out, ",supervisor,\"\",s2,resumed «birch» reply\n") {
		t.Fatalf("out = %q", out)
	}
}

func TestHistoryImportRedactsASecretShapeBeforeItIsStored(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, secret, mark string }{
		{"github token", "ghp_" + strings.Repeat("q", 36), "[redacted:github-token]"},
		{"bearer token", "Bearer " + strings.Repeat("b", 30), "[redacted:bearer]"},
		{"private key", "-----BEGIN " + "PRIVATE KEY-----\n" + strings.Repeat("k", 40) + "\n-----END " + "PRIVATE KEY-----", "[redacted:private-key]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := newImportRig(t)
			body := strings.TrimPrefix(c.secret, "Bearer ")
			g.sup(1, "claude", "sc", "2026-11-10T09:00:00Z", "")
			g.claudeFile("sc", importUser("2026-11-10T10:00:00.000Z", "go"), importReply("2026-11-10T10:00:05.000Z", "the secret is "+c.secret+" end"))
			out := g.h.ok("history", "import")
			got := g.stored()
			if strings.Contains(out, body) || len(got) != 1 || strings.Contains(got[0], body) || !strings.Contains(got[0], c.mark) {
				t.Fatalf("out = %q, stored = %q", out, got)
			}
		})
	}
}

func TestHistoryImportReportsAStoreErrorAndAReRunInsertsTheRest(t *testing.T) {
	t.Parallel()
	g := newImportRig(t)
	g.sup(1, "claude", "big", "2026-11-10T09:00:00Z", "")
	var lines []string
	for i := 0; i < 600; i++ {
		at := fmt.Sprintf("2026-11-10T10:%02d:%02d.000Z", i/60, i%60)
		lines = append(lines, importUser(at, "go"), importReply(at, fmt.Sprintf("reply %d", i)))
	}
	g.claudeFile("big", lines...)
	g.exec(`CREATE TRIGGER boom BEFORE INSERT ON supervisor_reply WHEN NEW.body = 'reply 550' BEGIN SELECT RAISE(ABORT, 'boom'); END`)
	if _, _, code := g.h.run("history", "import"); code == 0 {
		t.Fatalf("a store error exited 0")
	}
	if n := g.count(`SELECT count(*) FROM supervisor_reply`); n != 500 {
		t.Fatalf("rows after the error = %d", n)
	}
	g.exec(`DROP TRIGGER boom`)
	if out := g.h.ok("history", "import"); !strings.Contains(out, "big,600,100,ok") {
		t.Fatalf("rerun = %q", out)
	}
}

func TestHistoryImportDryRunCountsOnlyTheNamedSessionWhenTwoShareATimestamp(t *testing.T) {
	t.Parallel()
	g := newImportRig(t)
	g.sup(1, "claude", "a", "2026-11-10T09:00:00Z", "")
	g.sup(2, "claude", "b", "2026-11-10T09:00:00Z", "")
	g.claudeFile("a", importUser("2026-11-10T10:00:00.000Z", "go"), importReply("2026-11-10T10:00:05.000Z", "a body"))
	g.claudeFile("b", importUser("2026-11-10T10:00:00.000Z", "go"), importReply("2026-11-10T10:00:05.000Z", "b body"))
	g.h.ok("history", "import", "--session", "a")
	out := g.h.ok("history", "import", "--dry-run")
	if !strings.Contains(out, "\n  s2,claude,b,1,1,ok\n") || !strings.Contains(out, "1 would be inserted") {
		t.Fatalf("%q", out)
	}
}

func TestHistoryImportAttributesAnEntryOutsideEveryRowOfItsSessionToTheFirstRowNotToAnotherSession(t *testing.T) {
	t.Parallel()
	g := newImportRig(t)
	g.sup(1, "claude", "sc", "2026-11-10T09:00:00Z", "2026-11-10T10:00:00Z")
	g.sup(2, "codex", "sx", "2026-11-10T13:00:00Z", "2026-11-10T14:00:00Z")
	g.claudeFile("sc", importUser("2026-11-10T13:29:00.000Z", "go"), importReply("2026-11-10T13:30:00.000Z", "late reply"))
	g.h.ok("history", "import")
	if got := g.stored(); len(got) != 1 || got[0] != "s1 sc 2026-11-10T13:30:00.000Z 0 late reply" {
		t.Fatalf("stored = %q", got)
	}
}

func TestHistoryImportAttributesALateFlushOfAResumedSessionToItsLastRowAndAnEarlyOneToTheNextRow(t *testing.T) {
	t.Parallel()
	g := newImportRig(t)
	g.sup(1, "claude", "sc", "2026-11-17T04:00:00Z", "2026-11-17T05:00:00Z")
	g.sup(2, "claude", "sc", "2026-11-17T05:30:00Z", "2026-11-17T06:00:00Z")
	g.claudeFile("sc",
		importUser("2026-11-17T03:00:00.000Z", "go"),
		importReply("2026-11-17T03:30:00.000Z", "before every row"),
		importReply("2026-11-17T04:10:00.000Z", "in the first row"),
		importReply("2026-11-17T05:10:00.000Z", "between the rows"),
		importReply("2026-11-17T05:40:00.000Z", "in the second row"),
		importReply("2026-11-17T07:00:00.000Z", "after the last row"),
	)
	g.h.ok("history", "import")
	want := []string{
		"s1 sc 2026-11-17T03:30:00.000Z 0 before every row",
		"s1 sc 2026-11-17T04:10:00.000Z 0 in the first row",
		"s2 sc 2026-11-17T05:10:00.000Z 0 between the rows",
		"s2 sc 2026-11-17T05:40:00.000Z 0 in the second row",
		"s2 sc 2026-11-17T07:00:00.000Z 0 after the last row",
	}
	if got := g.stored(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("stored = %q", got)
	}
}

func (g *importRig) opencode(script string) string {
	g.t.Helper()
	if runtime.GOOS == "windows" {
		g.t.Skip("the fake opencode is a shell script")
	}
	dir := g.t.TempDir()
	if err := fakebin.WriteExecutable(filepath.Join(dir, "opencode"), []byte("#!/bin/sh\n"+script+"\n")); err != nil {
		g.t.Fatal(err)
	}
	g.h.vars["PATH"] = dir + ":/usr/bin:/bin"
	return dir
}

const opencodeExport = `cat <<'EOF'
{"info":{},"messages":[{"type":"user","text":"hi","time":{"created":1793260115879}},{"type":"assistant","finish":"stop","time":{"created":1793260115928},"content":[{"type":"text","text":"opencode reply"}]}]}
EOF`

func TestHistoryImportLeavesALiveOpencodeSupervisorToTheWatcherAndReadsAnEndedOne(t *testing.T) {
	t.Parallel()
	g := newImportRig(t)
	dir := g.opencode("touch \"$(dirname \"$0\")/called\"\n" + opencodeExport)
	g.exec(`INSERT INTO supervisor(id, harness, model, effort, argv, session, status, created_at) VALUES (1, 'opencode', 'm', 'low', '[]', 'ses_live', 'running', '2026-11-10T09:00:00Z')`)
	for _, args := range [][]string{{"history", "import"}, {"history", "import", "--dry-run"}} {
		out := g.h.ok(args...)
		if !strings.Contains(out, "s1,opencode,ses_live,0,0,still live\n") {
			t.Fatalf("%v: %q", args, out)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "called")); err == nil {
		t.Fatalf("the opencode binary ran for a live supervisor")
	}
	if n := g.count(`SELECT count(*) FROM supervisor_reply`); n != 0 {
		t.Fatalf("rows = %d", n)
	}
	g.sup(2, "opencode", "ses_done", "2026-11-10T10:00:00Z", "2026-11-10T11:00:00Z")
	out := g.h.ok("history", "import")
	if !strings.Contains(out, "s2,opencode,ses_done,1,1,ok\n") || !strings.Contains(out, "s1,opencode,ses_live,0,0,still live\n") {
		t.Fatalf("%q", out)
	}
}

func TestHistoryImportStopsTheRowsAt16KiBAndSaysSo(t *testing.T) {
	t.Parallel()
	g := newImportRig(t)
	g.exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 600) INSERT INTO supervisor(id, harness, model, effort, argv, status, created_at) SELECT i, 'claude', 'm', 'low', '[]', 'stopped', '2026-11-10T09:00:00Z' FROM n`)
	out := g.h.ok("history", "import", "--dry-run")
	if len(out) > 16<<10 || !strings.Contains(out, "total: 600 sessions; 0 entries; 0 would be inserted\n") || !strings.Contains(out, "more sessions not shown; the total counts them all.") {
		t.Fatalf("%d bytes: %.300q", len(out), out)
	}
}
