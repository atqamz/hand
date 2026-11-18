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
	for _, s := range []string{"", "one\ntwo\n", strings.Repeat("a\n", reportShowBytes/2), strings.Repeat("a", 1000) + "\n"} {
		if got := elide(s, reportShowBytes, elideCmd); got != s {
			t.Fatalf("elide changed a text of %d bytes", len(s))
		}
	}
}

func TestElidePrintsWholeWhenTheCutSavesNothing(t *testing.T) {
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
	if want := len(body) - (len(got) - len(marker) - 3); omitted != want {
		t.Fatalf("omitted = %d, want %d", omitted, want)
	}
}

func TestElideKeepsTheTailOfOneHugeLineAfterAShortFirstLine(t *testing.T) {
	body := "Summary\n" + strings.Repeat("x", 40000) + "\nVerdict: stuck\n"
	got := elide(body, reportShowBytes, elideCmd)
	head, _, after, _ := markerLine(t, got)
	if len(head) < reportShowBytes*3/4 {
		t.Fatalf("head is %d bytes of %d allowed", len(head), reportShowBytes)
	}
	if !strings.HasSuffix(after, "\nVerdict: stuck\n") || len(got) > reportShowBytes+200 {
		t.Fatalf("tail lost or result too big (%d bytes)", len(got))
	}
}

func TestElideCutsLongLinesOnRuneBoundaries(t *testing.T) {
	for _, r := range []string{"é", "🚀"} {
		got := elide(strings.Repeat(r, 2000), 1001, elideCmd)
		head, _, after, _ := markerLine(t, got)
		if !utf8.ValidString(head) || !utf8.ValidString(after) || len(head) > 1001 || len(head) < 800 || len(after) < 100 {
			t.Fatalf("%q: head %d bytes, tail %d bytes, valid cut %v", r, len(head), len(after), utf8.ValidString(head+after))
		}
	}
}

func TestElideBoundsTheDeliveredLinesItKeeps(t *testing.T) {
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

func TestReportFooterStaysUnderThePromptCap(t *testing.T) {
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
