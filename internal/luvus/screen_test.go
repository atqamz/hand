package luvus

import (
	"strings"
	"testing"
)

func TestScreenDigestIgnoresTheAutoDenyCountdown(t *testing.T) {
	early := "Bash command\n  rm -f $r/$f\n Claude Code will automatically deny this request in 1:59, to avoid blocking progress\n Do you want to proceed?\n❯ 1. Yes   \n  2. No"
	late := "Bash command\n  rm -f $r/$f\n Claude Code will automatically deny this request in 1:12, to avoid blocking progress\n Do you want to proceed?\n❯ 1. Yes\n  2. No"
	if ScreenDigest(early) != ScreenDigest(late) {
		t.Fatal("the countdown and trailing spaces changed the digest")
	}
	other := "Bash command\n  rm -f $r/$f\n Claude Code will automatically deny this request in 1:12, to avoid blocking progress\n Do you want to make this edit?\n❯ 1. Yes\n  2. No"
	if ScreenDigest(late) == ScreenDigest(other) {
		t.Fatal("a different question kept the digest")
	}
	if len(ScreenDigest(late)) != 32 {
		t.Fatalf("digest %q is not 16 hex bytes", ScreenDigest(late))
	}
}

func TestScreenDigestReadsOnlyTheQuestionBox(t *testing.T) {
	rule := strings.Repeat("─", 40)
	box := rule + "\n Bash command\n   rm -f $r/$f\n Claude Code will automatically deny this request in 1:31\n Do you want to proceed?\n❯ 1. Yes\n  2. No\n Esc to cancel · Tab to amend"
	blink := "  Removing the file\n  ⎿  $ export r=/tmp/x\n" + box
	lit := "● Removing the file\n  ⎿  $ rm -f $r/$f\n" + strings.Replace(box, "1:31", "1:29", 1)
	if ScreenDigest(blink) != ScreenDigest(lit) {
		t.Fatal("the running tool's blinking lines above the question changed the digest")
	}
	if ScreenDigest(blink) == ScreenDigest(strings.Replace(blink, "Do you want to proceed?", "Do you want to make this edit?", 1)) {
		t.Fatal("a different question kept the digest")
	}
}
