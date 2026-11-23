package cli

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/atqamz/hand/internal/harness"
)

const elideCmd = "hand report show --full r1"

func markerLine(t *testing.T, out string) (head, marker, after string, omitted int) {
	t.Helper()
	i := strings.Index(out, "\n… ")
	if i < 0 {
		t.Fatalf("no elision line in %d bytes", len(out))
	}
	head = out[:i]
	marker, after, _ = strings.Cut(out[i+1:], "\n")
	fields := strings.Fields(marker)
	n, err := strconv.Atoi(fields[1])
	if err != nil || fields[2] != "bytes" || !strings.Contains(marker, "`"+elideCmd+"` prints all") {
		t.Fatalf("elision line = %q", marker)
	}
	return head, marker, after, n
}

func TestElideLeavesAShortTextAlone(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", "one\ntwo\n", strings.Repeat("a\n", reportShowBytes/2), strings.Repeat("a", 1000) + "\n"} {
		if got := elide(s, reportShowBytes, elideCmd); got != s {
			t.Fatalf("elide changed a text of %d bytes", len(s))
		}
	}
}

func TestElidePrintsWholeWhenTheCutSavesNothing(t *testing.T) {
	t.Parallel()
	for _, over := range []int{1, 30} {
		s := strings.Repeat("a", 1000+over)
		if got := elide(s, 1000, elideCmd); got != s {
			t.Fatalf("%d bytes over: printed %d bytes for %d", over, len(got), len(s))
		}
	}
	s := strings.Repeat("a", 3000)
	if got := elide(s, 1000, elideCmd); len(got) >= len(s) || !strings.Contains(got, "bytes omitted") {
		t.Fatalf("a text far over the limit is not cut: %d bytes", len(got))
	}
}

func TestElideKeepsTheHeadTheTailAndDeliveredLines(t *testing.T) {
	t.Parallel()
	x := strings.Repeat("x", 99) + "\n"
	body := "Fixed login\n" + strings.Repeat(x, 50) + "Delivered: /abs/mid.md\n" + strings.Repeat(x, 50) + "Gate: CHANGES\nDelivered: https://example.com/pr/1\n"
	got := elide(body, 1000, elideCmd)
	head, marker, after, omitted := markerLine(t, got)
	if !strings.HasPrefix(head, "Fixed login\n") || strings.Contains(head, "Gate") {
		t.Fatalf("head = %q", head[:20])
	}
	for _, l := range strings.Split(head[len("Fixed login\n"):], "\n") {
		if l != strings.Repeat("x", 99) {
			t.Fatalf("a head line is cut: %q", l)
		}
	}
	lines := strings.Split(strings.TrimRight(after, "\n"), "\n")
	if lines[0] != "Delivered: /abs/mid.md" {
		t.Fatalf("kept delivered line = %q", lines[0])
	}
	n := len(lines)
	if strings.Trim(lines[n-3], "x") != "" || lines[n-2] != "Gate: CHANGES" || lines[n-1] != "Delivered: https://example.com/pr/1" {
		t.Fatalf("tail = %q", lines[n-3:])
	}
	if len(got) > 1000+len(marker)+100 {
		t.Fatalf("result is %d bytes", len(got))
	}
	if want := len(body) - len(head) - len(after); omitted != want {
		t.Fatalf("omitted = %d, want %d", omitted, want)
	}
}

func TestElideCountsKeptDeliveredLinesAsShown(t *testing.T) {
	t.Parallel()
	x := strings.Repeat("x", 99) + "\n"
	body := strings.Repeat(x, 30) + strings.Repeat("Delivered: /abs/f\n", 5) + strings.Repeat(x, 30)
	head, _, after, omitted := markerLine(t, elide(body, 1000, elideCmd))
	if strings.Count(after, "Delivered:") != 5 {
		t.Fatalf("after = %q", after)
	}
	if want := len(body) - len(head) - len(after); omitted != want {
		t.Fatalf("omitted = %d, want %d", omitted, want)
	}
}

func TestElideKeepsTheTailOfOneHugeLineAfterAShortFirstLine(t *testing.T) {
	t.Parallel()
	for _, first := range []int{7, 3000} {
		body := strings.Repeat("s", first) + "\n" + strings.Repeat("x", 40000) + "\nVerdict: stuck\n"
		got := elide(body, reportShowBytes, elideCmd)
		head, _, after, _ := markerLine(t, got)
		if len(head) < reportShowBytes*3/4 {
			t.Fatalf("first line %d: head is %d bytes of %d allowed", first, len(head), reportShowBytes)
		}
		if !strings.HasSuffix(after, "\nVerdict: stuck\n") || len(got) > reportShowBytes+200 {
			t.Fatalf("first line %d: tail lost or result too big (%d bytes)", first, len(got))
		}
	}
}

func TestElideCutsTheHeadAtTheLastLineThatFits(t *testing.T) {
	t.Parallel()
	const limit, share = 1256, 1256 - 157
	for _, c := range []struct {
		line int
		want int
	}{{100, share}, {367, 733}} {
		body := strings.Repeat(strings.Repeat("h", c.line-1)+"\n", 30)
		head, _, _, _ := markerLine(t, elide(body, limit, elideCmd))
		if len(head) != c.want {
			t.Fatalf("lines of %d bytes: head is %d bytes, want %d", c.line, len(head), c.want)
		}
	}
}

func TestElideStartsTheTailAtTheFirstLineItKeepsWhole(t *testing.T) {
	t.Parallel()
	const limit, tail = 1000, 125
	prefix := strings.Repeat("a", 3000) + "\n"
	for _, c := range []struct {
		name, text string
		want       int
	}{
		{"line start at the tail", strings.Repeat("t", tail), tail},
		{"line break one byte before", "u" + strings.Repeat("t", tail), tail},
		{"blank line first", "\n" + strings.Repeat("t", tail-1), tail - 1},
		{"blank CRLF line first", "\r\n" + strings.Repeat("t", tail-2), tail - 2},
	} {
		_, _, after, _ := markerLine(t, elide(prefix+c.text, limit, elideCmd))
		if len(after) != c.want || strings.HasPrefix(after, "\n") || strings.HasPrefix(after, "\r") {
			t.Fatalf("%s: tail is %d bytes %q, want %d", c.name, len(after), after[:min(len(after), 8)], c.want)
		}
	}
}

func TestElideTrimsCRLFFromTheHeadAndKeptLines(t *testing.T) {
	t.Parallel()
	x := strings.Repeat("x", 98) + "\r\n"
	body := strings.Repeat(x, 30) + "Delivered: /abs/mid.md\r\n" + strings.Repeat(x, 30)
	head, _, after, _ := markerLine(t, elide(body, 1000, elideCmd))
	if strings.HasSuffix(head, "\r") || !strings.HasPrefix(after, "Delivered: /abs/mid.md\n") {
		t.Fatalf("head ends %q, after starts %q", head[len(head)-3:], after[:30])
	}
}

func TestElideKeepsListedDeliveredLines(t *testing.T) {
	t.Parallel()
	x := strings.Repeat("x", 99) + "\n"
	body := strings.Repeat(x, 30) + "- Delivered: /a\n* Delivered: /b\n \tDelivered: /c\n" + strings.Repeat(x, 30)
	_, _, after, _ := markerLine(t, elide(body, 1000, elideCmd))
	if !strings.HasPrefix(after, "- Delivered: /a\n* Delivered: /b\n \tDelivered: /c\n") {
		t.Fatalf("after = %q", after[:60])
	}
}

func TestElideCutsLongLinesOnRuneBoundaries(t *testing.T) {
	t.Parallel()
	for _, r := range []string{"é", "🚀"} {
		got := elide(strings.Repeat(r, 2000), 1000, elideCmd)
		head, _, after, _ := markerLine(t, got)
		if !utf8.ValidString(head) || !utf8.ValidString(after) || len(head) > 1000 || len(head) < 800 || len(after) < 100 {
			t.Fatalf("%q: head %d bytes, tail %d bytes, valid cut %v", r, len(head), len(after), utf8.ValidString(head+after))
		}
	}
}

func TestElideBoundsTheDeliveredLinesItKeeps(t *testing.T) {
	t.Parallel()
	x := strings.Repeat("x", 99) + "\n"
	many := strings.Repeat("Delivered: /abs/file\n", 500)
	big := "Delivered: " + strings.Repeat("y", 5000) + "\n"
	body := strings.Repeat(x, 20) + many + big + strings.Repeat(x, 100)
	got := elide(body, 2000, elideCmd)
	if len(got) > 2000+300 {
		t.Fatalf("result is %d bytes", len(got))
	}
	if strings.Contains(got, strings.Repeat("y", 100)) {
		t.Fatal("a huge delivered line was kept")
	}
}

func TestElideKeepsDeliveredLinesUpToExactlyAEighthOfTheLimit(t *testing.T) {
	t.Parallel()
	x := strings.Repeat("x", 99) + "\n"
	for _, c := range []struct {
		name  string
		lines []string
		want  int
	}{
		{"second line is one byte over", []string{"Delivered: " + strings.Repeat("a", 51), "Delivered: " + strings.Repeat("b", 51)}, 1},
		{"one line fills the share", []string{"Delivered: " + strings.Repeat("c", 113)}, 1},
		{"one line is a byte over", []string{"Delivered: " + strings.Repeat("c", 114)}, 0},
	} {
		body := strings.Repeat(x, 20) + strings.Join(c.lines, "\n") + "\n" + strings.Repeat(x, 30)
		_, _, after, _ := markerLine(t, elide(body, 1000, elideCmd))
		if got := strings.Count(after, "Delivered:"); got != c.want {
			t.Fatalf("%s: kept %d lines, want %d", c.name, got, c.want)
		}
	}
}

func TestReportFooterStaysUnderThePromptCap(t *testing.T) {
	t.Parallel()
	long := "C:/" + strings.Repeat("p", 200) + "/hand.exe"
	for _, goos := range []string{"linux", "windows"} {
		footer := reportFooterFor(goos, long)
		if len(footer) > harness.MaxPromptBytes/4 {
			t.Errorf("%s footer is %d bytes", goos, len(footer))
		}
		if !strings.Contains(footer, bigOutputNote) {
			t.Errorf("%s footer lacks the big output note", goos)
		}
	}
}
