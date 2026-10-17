package board

import (
	"slices"
	"strings"
	"testing"
)

func TestOptionsReadTheSupervisorsFormat(t *testing.T) {
	body := "Pick how CI follows luvus.\n\n1. Pin 0.14.2 — reproducible builds; we bump it by hand (recommended)\n2) Track 0.14.x – patch fixes arrive by themselves\n3. Track the latest release - newest features\n4. Re-run the job"
	want := []option{
		{Key: "1", Label: "Pin 0.14.2", Note: "reproducible builds; we bump it by hand", Value: "1. Pin 0.14.2", Recommended: true},
		{Key: "2", Label: "Track 0.14.x", Note: "patch fixes arrive by themselves", Value: "2. Track 0.14.x"},
		{Key: "3", Label: "Track the latest release", Note: "newest features", Value: "3. Track the latest release"},
		{Key: "4", Label: "Re-run the job", Value: "4. Re-run the job"},
	}
	if got := options(body); !slices.Equal(got, want) {
		t.Fatalf("options = %+v, want %+v", got, want)
	}
}

func TestOptionsTakeRecommendedInAnyCaseAndPosition(t *testing.T) {
	got := options("1. Yes (Recommended)\n2. No — keeps the old name (RECOMMENDED) for now")
	if len(got) != 2 || !got[0].Recommended || got[0].Label != "Yes" || !got[1].Recommended || got[1].Note != "keeps the old name for now" {
		t.Fatalf("options = %+v", got)
	}
}

func TestOptionsMarkOtherAtItsWordOnly(t *testing.T) {
	got := options("1. Other (write your own)\n2. Otherwise ask again\n3. other things")
	if len(got) != 3 || !got[0].Other || got[1].Other || !got[2].Other {
		t.Fatalf("options = %+v", got)
	}
}

func TestOptionsAcceptLettersAndBothDelimiters(t *testing.T) {
	got := options("A. Ship it\nB) Wait — until Friday\n  C. Indented")
	keys := []string{got[0].Key, got[1].Key, got[2].Key}
	if len(got) != 3 || !slices.Equal(keys, []string{"A", "B", "C"}) || got[1].Value != "B. Wait" || got[1].Note != "until Friday" {
		t.Fatalf("options = %+v", got)
	}
}

func TestOptionsSkipCodeFences(t *testing.T) {
	body := "Pick one\n\n```\n1. make build\n2. make test\n```\n\n1. Ship it\n2. Wait"
	if got := options(body); len(got) != 2 || got[0].Label != "Ship it" {
		t.Fatalf("options = %+v", got)
	}
	if got := prose(body); !strings.Contains(got, "1. make build\n2. make test") || strings.Contains(got, "Ship it") || strings.Contains(got, "Wait") {
		t.Fatalf("prose = %q", got)
	}
}

func TestOptionsLeaveAHyphenatedWordAlone(t *testing.T) {
	got := options("1. Re-run the nightly job\n2. A well-known fix — cheap")
	if got[0].Note != "" || got[0].Label != "Re-run the nightly job" || got[1].Label != "A well-known fix" || got[1].Note != "cheap" {
		t.Fatalf("options = %+v", got)
	}
}

func TestOptionsSetALongLabelAtNormalWeight(t *testing.T) {
	long := strings.Repeat("é", 91)
	exact := strings.Repeat("é", 90)
	got := options("1. " + long + "\n2. " + exact)
	if len(got) != 2 || !got[0].Long || got[1].Long {
		t.Fatalf("options = %+v", got)
	}
}

func TestOptionsKeepMarkdownInTheValue(t *testing.T) {
	got := options("1. **Rename** the cookie — `Set-Cookie` changes once")
	if len(got) != 1 || got[0].Value != "1. **Rename** the cookie" || got[0].Note != "`Set-Cookie` changes once" {
		t.Fatalf("options = %+v", got)
	}
}

func TestABodyWithoutNumberedLinesHasNoOptions(t *testing.T) {
	body := "Ship it?\n\nCI is green.\n- one\n- two\n1 not a number line\nA word. Not an option"
	if got := options(body); got != nil {
		t.Fatalf("options = %+v", got)
	}
	if got := prose(body); got != body {
		t.Fatalf("prose = %q, want the body untouched", got)
	}
	if got := options(""); got != nil {
		t.Fatalf("options of nothing = %+v", got)
	}
}

func TestProseDropsOnlyTheOptionLines(t *testing.T) {
	body := "Context line.\n\n1. A\n2. B\n\nAfter the list."
	if got, want := prose(body), "Context line.\n\n\nAfter the list."; got != want {
		t.Fatalf("prose = %q, want %q", got, want)
	}
	if got := prose("1. A\n2. B"); got != "" {
		t.Fatalf("prose of only options = %q", got)
	}
}

func TestASecondListThatReusesAKeyStaysInTheQuestion(t *testing.T) {
	body := "Pick one\n\n1. A\n2. B\n\nThen the steps:\n1. do x\n2. do y"
	got := options(body)
	if len(got) != 2 || got[0].Label != "A" || got[1].Label != "B" {
		t.Fatalf("options = %+v", got)
	}
	if rest := prose(body); !strings.Contains(rest, "Then the steps:\n1. do x\n2. do y") || strings.Contains(rest, "1. A") {
		t.Fatalf("prose = %q", rest)
	}
}

func TestALineWithoutALabelIsNeitherAnOptionNorLostFromTheQuestion(t *testing.T) {
	body := "Pick one\n\n1. (recommended)\n2. B\n3. C"
	if got := options(body); len(got) != 2 || got[0].Key != "2" || got[1].Key != "3" {
		t.Fatalf("options = %+v", got)
	}
	if rest := prose(body); !strings.Contains(rest, "1. (recommended)") || strings.Contains(rest, "2. B") {
		t.Fatalf("prose = %q", rest)
	}
}

func TestChosenAndNoteReadAnAnswerBack(t *testing.T) {
	opts := options("1. Pin\n2. Track\nC. Other (write your own)")
	cases := []struct{ answer, key, note string }{
		{"2. Track", "2", ""},
		{"2. Track\n\nuse the patch channel", "2", "use the patch channel"},
		{"C. Other (write your own)\n\nvendor it", "C", "vendor it"},
		{"yes, pin it", "", "yes, pin it"},
		{"9. Nine\n\nnot an option here", "", "9. Nine\n\nnot an option here"},
		{"2", "", "2"},
		{"", "", ""},
	}
	for _, c := range cases {
		if gotKey, gotNote := chosen(opts, c.answer), note(opts, c.answer); gotKey != c.key || gotNote != c.note {
			t.Errorf("answer %q: chosen %q, note %q, want %q, %q", c.answer, gotKey, gotNote, c.key, c.note)
		}
	}
	if chosen(nil, "2. Track") != "" || note(nil, "2. Track") != "2. Track" {
		t.Fatal("an answer to a question without options is all note")
	}
}
