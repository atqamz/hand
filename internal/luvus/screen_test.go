package luvus

import "testing"

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
