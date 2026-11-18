package cli

import (
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/harness"
)

func TestElideLeavesAShortTextAlone(t *testing.T) {
	for _, s := range []string{"", "one\ntwo\n", strings.Repeat("a\n", reportShowBytes/2)} {
		if got := elide(s, reportShowBytes, "--full"); got != s {
			t.Fatalf("elide changed a text of %d bytes", len(s))
		}
	}
}

func TestElideCutsAtALineAndKeepsDeliveredLines(t *testing.T) {
	line := strings.Repeat("x", 99) + "\n"
	body := "Fixed login\n" + strings.Repeat(line, 100) + "Delivered: https://example.com/pr/1\n- Delivered: /abs/doc.md\n"
	got := elide(body, 1000, "hand report show --full r1")
	lines := strings.Split(got, "\n")
	if lines[0] != "Fixed login" {
		t.Fatalf("summary lost: %q", lines[0])
	}
	n := len(lines)
	if lines[n-2] != "Delivered: https://example.com/pr/1" || lines[n-1] != "- Delivered: /abs/doc.md" {
		t.Fatalf("delivered lines not last: %q", lines[n-3:])
	}
	note := lines[n-3]
	if !strings.HasPrefix(note, "… ") || !strings.Contains(note, "bytes omitted") || !strings.Contains(note, "`hand report show --full r1`") {
		t.Fatalf("elision line = %q", note)
	}
	for _, l := range lines[1 : n-3] {
		if l != strings.Repeat("x", 99) {
			t.Fatalf("a kept line is cut: %q", l)
		}
	}
	if len(got) > 1000+200 {
		t.Fatalf("result is %d bytes", len(got))
	}
}

func TestElideCutsALongLineOnARuneBoundary(t *testing.T) {
	got := elide(strings.Repeat("é", 2000), 1001, "--full")
	head, _, _ := strings.Cut(got, "\n")
	if len(head) > 1001 || strings.ContainsRune(head, '\uFFFD') || strings.Count(head, "é")*2 != len(head) {
		t.Fatalf("head = %d bytes, invalid rune cut", len(head))
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
