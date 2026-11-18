package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

var tailNow = time.Date(2026, 11, 18, 12, 17, 42, 0, time.UTC)

func tailTurn(role, text string) transcript.Entry {
	return transcript.Entry{Role: role, Text: text, At: "2026-11-18T12:14:00Z"}
}

func TestLastTurnsKeepsTheNewestSixOperatorAndSupervisorEntries(t *testing.T) {
	var in []transcript.Entry
	for i := 1; i <= 7; i++ {
		in = append(in, tailTurn("operator", fmt.Sprintf("turn %d", i)), tailTurn("hand", fmt.Sprintf("wake %d", i)))
	}
	got := lastTurns("s74", tailNow, in)
	if strings.Contains(got, "wake") || strings.Contains(got, "turn 1\n") || !strings.Contains(got, "turn 2\n") || !strings.Contains(got, "turn 7") || strings.Count(got, "\n## operator") != 6 {
		t.Fatalf("%s", got)
	}
	if !strings.HasPrefix(got, "# Last turns of s74\n\nHand copied these from the session s74 ended at 2026-11-18T12:17:42Z. Verbatim, newest last.") || !strings.Contains(got, "## operator, 2026-11-18T12:14:00Z\n\nturn 2") {
		t.Fatalf("%s", got)
	}
	if strings.Index(got, "turn 2") > strings.Index(got, "turn 7") {
		t.Fatalf("not newest last: %s", got)
	}
}

func TestLastTurnsIsEmptyWithoutOperatorOrSupervisorText(t *testing.T) {
	for _, in := range [][]transcript.Entry{nil, {tailTurn("hand", "wake")}, {tailTurn("operator", " \r\n\t "), tailTurn("hand", "x")}} {
		if got := lastTurns("s1", tailNow, in); got != "" {
			t.Fatalf("%q", got)
		}
	}
}

func TestLastTurnsNormalisesLineEndingsAndSkipsEmptyEntries(t *testing.T) {
	got := lastTurns("s1", tailNow, []transcript.Entry{tailTurn("operator", "  a\r\nb  "), tailTurn("supervisor", "\n\n")})
	if strings.Contains(got, "\r") || !strings.Contains(got, "\n\na\nb\n") || strings.Contains(got, "## supervisor") {
		t.Fatalf("%q", got)
	}
}

func TestLastTurnsCutsTheMiddleOfAnOversizedNewestEntryAndKeepsItsEnd(t *testing.T) {
	text := "START " + strings.Repeat("filler ", 2000) + "Which one should I brief?"
	got := lastTurns("s1", tailNow, []transcript.Entry{tailTurn("operator", "older"), tailTurn("supervisor", text)})
	if !strings.Contains(got, "START ") || !strings.HasSuffix(got, "Which one should I brief?\n") || !strings.Contains(got, "bytes cut …]") {
		t.Fatalf("%s", got)
	}
	if strings.Contains(got, "## operator") {
		t.Fatalf("the walk went on past the cut: %s", got)
	}
	if len(got) > 4096 {
		t.Fatalf("%d bytes", len(got))
	}
	var cut int
	fmt.Sscanf(got[strings.Index(got, "[… ")+len("[… "):], "%d", &cut)
	if body := got[strings.Index(got, "START"):]; len(body) != len(text)-cut+len("\n[…  bytes cut …]\n")+len(fmt.Sprint(cut))+1 {
		t.Fatalf("cut count %d does not match: %d vs %d", cut, len(body), len(text))
	}
}

func TestLastTurnsKeepsMoreOfTheEndThanOfTheStart(t *testing.T) {
	text := strings.Repeat("a", 5000) + strings.Repeat("b", 5000)
	got := lastTurns("s1", tailNow, []transcript.Entry{tailTurn("supervisor", text)})
	if a, b := strings.Count(got, "a")-strings.Count(got[:strings.Index(got, "\n\n## ")], "a"), strings.Count(got, "b"); b <= a {
		t.Fatalf("a=%d b=%d", a, b)
	}
}

func TestLastTurnsCutsOnRuneBoundaries(t *testing.T) {
	for shift := 0; shift < 4; shift++ {
		text := strings.Repeat("a", shift) + strings.Repeat("日本語", 2000) + strings.Repeat("é", 10)
		got := lastTurns("s1", tailNow, []transcript.Entry{tailTurn("supervisor", text)})
		if !utf8.ValidString(got) {
			t.Fatalf("shift %d: invalid UTF-8", shift)
		}
	}
}

func TestLastTurnsStopsAtAnOversizedEntryInTheMiddle(t *testing.T) {
	big := strings.Repeat("x", 4000)
	got := lastTurns("s1", tailNow, []transcript.Entry{tailTurn("operator", "oldest"), tailTurn("supervisor", big), tailTurn("operator", "newest")})
	if !strings.Contains(got, "newest") || !strings.Contains(got, "bytes cut") || strings.Contains(got, "oldest") {
		t.Fatalf("%s", got)
	}
}

func TestLastTurnsRemovesASecretBeforeTheCut(t *testing.T) {
	token := "ghp_" + strings.Repeat("Z", 40)
	left := tailBytes - tailBytes/3
	for offset := -50; offset <= 50; offset += 5 {
		text := strings.Repeat("a", 2000) + " " + strings.Repeat("b", 2000+offset) + " " + token + strings.Repeat("c", left-45+offset)
		got := lastTurns("s1", tailNow, []transcript.Entry{tailTurn("supervisor", text)})
		if strings.Contains(got, "ghp_") || strings.Contains(got, strings.Repeat("Z", 8)) {
			t.Fatalf("offset %d: part of the token survives", offset)
		}
	}
	got := lastTurns("s1", tailNow, []transcript.Entry{tailTurn("operator", "use "+token)})
	if !strings.Contains(got, "use [redacted:github-token]") {
		t.Fatalf("%s", got)
	}
}

func TestLastTurnsStaysUnderTheSizePin(t *testing.T) {
	var in []transcript.Entry
	for i := 0; i < 10; i++ {
		in = append(in, tailTurn("supervisor", strings.Repeat("word ", 2000)))
	}
	if got := lastTurns("s100", tailNow, in); len(got) > 4096 {
		t.Fatalf("%d bytes", len(got))
	}
}

func TestTailOfFailsOpenOnAMissingOrUnreadableTranscript(t *testing.T) {
	home := t.TempDir()
	w := &watcher{r: &runner{home: home, env: Env{Now: func() time.Time { return tailNow }, Getenv: func(string) string { return "" }}}}
	sup := state.Supervisor{ID: 1, Harness: "claude", Session: "11111111-1111-4111-8111-111111111111"}
	ctx := context.Background()
	if _, err := w.tailOf(ctx, &transcript.Reader{Paths: transcript.Paths{Claude: t.TempDir()}}, sup); !errors.Is(err, transcript.ErrNoSession) {
		t.Fatalf("empty folder: %v", err)
	}
	claude := t.TempDir()
	dir := filepath.Join(claude, "projects", "p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sup.Session+".jsonl"), []byte("not json\nstill not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := w.tailOf(ctx, &transcript.Reader{Paths: transcript.Paths{Claude: claude}}, sup); err == nil {
		t.Fatal("a transcript that is not JSON gave a tail")
	}
	if err := os.WriteFile(filepath.Join(dir, sup.Session+".jsonl"), []byte(`{"type":"user","timestamp":"2026-09-26T00:00:01Z","message":{"content":"hi"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := w.tailOf(ctx, &transcript.Reader{Paths: transcript.Paths{Claude: claude}}, sup)
	if err != nil || !strings.Contains(got, "## operator, 2026-09-26T00:00:01Z\n\nhi") || !strings.Contains(got, "Last turns of s1") {
		t.Fatalf("%q, %v", got, err)
	}
}

func TestTailOfReadsAnOmpSession(t *testing.T) {
	agent := t.TempDir()
	body, err := os.ReadFile(filepath.Join("..", "transcript", "testdata", "omp-calm.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(agent, "sessions", "-fleets-demo")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "2026-09-28T01-00-00-000Z_omp-1.jsonl"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	getenv := func(k string) string {
		if k == "PI_CODING_AGENT_DIR" {
			return agent
		}
		return ""
	}
	w := &watcher{r: &runner{home: t.TempDir(), env: Env{Now: func() time.Time { return tailNow }, Getenv: getenv}}}
	got, err := w.tailOf(context.Background(), &transcript.Reader{Paths: transcript.Paths{Omp: agent}}, state.Supervisor{ID: 1, Harness: "omp", Session: "omp-1"})
	if err != nil || !strings.Contains(got, "## operator, 2026-09-28T01:00:") || !strings.Contains(got, "\n\nhi\n") || !strings.Contains(got, "\n\nHello.\n") || strings.Contains(got, "supervisor s1 started") {
		t.Fatalf("%q, %v", got, err)
	}
}

func TestWriteLastTurnsLeavesOnlyTheFileAndReplacesAnOldOne(t *testing.T) {
	home := t.TempDir()
	for _, text := range []string{"first\n", "second\n"} {
		if err := writeLastTurns(home, text); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(lastTurnsPath(home))
	if err != nil || string(got) != "second\n" {
		t.Fatalf("%q, %v", got, err)
	}
	entries, _ := os.ReadDir(filepath.Join(home, "memory"))
	if len(entries) != 1 || entries[0].Name() != "last-turns.md" {
		t.Fatalf("memory/ holds %v", entries)
	}
}
