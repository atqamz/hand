package board

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestShotsTakeOnlyThisFleetsInbox(t *testing.T) {
	home := t.TempDir()
	inbox := filepath.Join(home, "inbox")
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, size := range map[string]int{"20261001-120000-1.png": 10, "20261001-120000-3-report.pdf": 312320} {
		if err := os.WriteFile(filepath.Join(inbox, name), make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	other := filepath.Join(t.TempDir(), "inbox")
	text := strings.Join([]string{
		"look",
		"[image: " + filepath.Join(inbox, "20261001-120000-1.png") + "]",
		"[image: " + filepath.Join(other, "20261001-120000-2.png") + "]",
		"[image: /etc/x.png]",
		"[file: " + filepath.Join(inbox, "20261001-120000-3-report.pdf") + "]",
		"[file: " + filepath.Join(inbox, "20261001-120000-4-gone.tar.gz") + "]",
		"[file: " + inbox + "/../hand.db]",
		"[file: " + inbox + "/20261001-120000-5-../../hand.db]",
		"[file: /etc/passwd]",
	}, "\n")
	rest, files := shots(text, home, "")
	want := strings.Join([]string{
		"look",
		"[image: " + filepath.Join(other, "20261001-120000-2.png") + "]",
		"[image: /etc/x.png]",
		"[file: " + inbox + "/../hand.db]",
		"[file: " + inbox + "/20261001-120000-5-../../hand.db]",
		"[file: /etc/passwd]",
	}, "\n")
	if rest != want || len(files) != 3 {
		t.Fatalf("rest %q files %+v", rest, files)
	}
	if a := files[0]; a.Name != "20261001-120000-1.png" || a.Label != a.Name || !a.Image || a.Missing || a.Kind != "PNG" {
		t.Fatalf("image %+v", a)
	}
	if a := files[1]; a.Label != "report.pdf" || a.Image || a.Missing || a.Kind != "PDF" || a.Size != "305 KiB" || a.Path != filepath.Join(inbox, "20261001-120000-3-report.pdf") {
		t.Fatalf("file %+v", a)
	}
	if a := files[2]; a.Label != "gone.tar.gz" || !a.Missing || a.Kind != "GZ" {
		t.Fatalf("missing %+v", a)
	}
	if rest, files := shots("[image: "+filepath.Join(inbox, "20261001-120000-1.png")+"]\n", home, ""); rest != "" || len(files) != 1 {
		t.Fatalf("only an image: rest %q files %+v", rest, files)
	}
}

func TestShotsReadInlineMarkersAndKeepTheOldLines(t *testing.T) {
	home := t.TempDir()
	inbox := filepath.Join(home, "inbox")
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatal(err)
	}
	one, two, three := "20261001-120000-1.png", "20261001-120000-2-b.png", "20261001-120000-3-c.pdf"
	p := func(name string) string { return filepath.Join(inbox, name) }
	for _, c := range []struct {
		what  string
		text  string
		rest  string
		names []string
		ns    []int
	}{
		{"inline in place", "look at [image 1: " + p(one) + "], like [image 2: " + p(two) + "]", "look at [attachment 1], like [attachment 2]", []string{one, two}, []int{1, 2}},
		{"out of order keeps appearance order", "[file 2: " + p(three) + "] then [image 1: " + p(one) + "]", "[attachment 2] then [attachment 1]", []string{three, one}, []int{2, 1}},
		{"appended lines leave the text", "see [image 2: " + p(two) + "]\n[image 1: " + p(one) + "]\n", "see [attachment 2]", []string{two, one}, []int{2, 1}},
		{"a trailing line of only files", "look\n[image 1: " + p(one) + "]\n[file 2: " + p(three) + "]\n", "look", []string{one, three}, []int{1, 2}},
		{"an own-line token in the middle stays a token", "a\n[image 1: " + p(one) + "]\nb", "a\n[attachment 1]\nb", []string{one}, []int{1}},
		{"a repeated token at the end stays a token", "compare [image 1: " + p(one) + "]\nagain:\n[image 1: " + p(one) + "]", "compare [attachment 1]\nagain:\n[attachment 1]", []string{one}, []int{1}},
		{"the old line takes the next unused number", "[image: " + p(one) + "]\nsee [image 1: " + p(two) + "]", "see [attachment 1]", []string{one, two}, []int{2, 1}},
		{"two old lines count in order", "hi\n[image: " + p(one) + "]\n[file: " + p(three) + "]", "hi", []string{one, three}, []int{1, 2}},
		{"an old line mid sentence stays text", "a [image: " + p(one) + "] b", "a [image: " + p(one) + "] b", nil, nil},
		{"the same file twice has one shot", "[image 1: " + p(one) + "] and [image 1: " + p(one) + "]", "[attachment 1] and [attachment 1]", []string{one}, []int{1}},
		{"one number for two files renumbers the second", "[image 1: " + p(one) + "] [image 1: " + p(two) + "]", "[attachment 1] [attachment 2]", []string{one, two}, []int{1, 2}},
		{"another folder stays text", "[image 1: " + filepath.Join(t.TempDir(), "inbox", one) + "]", "", nil, nil},
		{"a number out of the grammar stays text", "[image 0: " + p(one) + "] [image 12345: " + p(one) + "]", "[image 0: " + p(one) + "] [image 12345: " + p(one) + "]", nil, nil},
		{"a path that only starts like the inbox", "[image 1: " + p(one) + "x]", "[image 1: " + p(one) + "x]", nil, nil},
	} {
		rest, files := shots(c.text, home, "")
		if c.rest == "" && len(c.names) == 0 {
			if len(files) != 0 {
				t.Errorf("%s: files %+v", c.what, files)
			}
			continue
		}
		var names []string
		var ns []int
		for _, f := range files {
			names, ns = append(names, f.Name), append(ns, f.N)
		}
		if rest != c.rest || !slices.Equal(names, c.names) || !slices.Equal(ns, c.ns) {
			t.Errorf("%s: rest %q names %v ns %v, want %q %v %v", c.what, rest, names, ns, c.rest, c.names, c.ns)
		}
	}
}

func TestShotsFindTheInboxWhenTheHomePathHoldsABracket(t *testing.T) {
	home := filepath.Join(t.TempDir(), "a]b [c]")
	inbox := filepath.Join(home, "inbox")
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatal(err)
	}
	name := "20261001-120000-1.png"
	for _, text := range []string{"[image: " + filepath.Join(inbox, name) + "]", "see [image 1: " + filepath.Join(inbox, name) + "] here"} {
		if _, files := shots(text, home, ""); len(files) != 1 || files[0].Name != name || files[0].N != 1 {
			t.Errorf("%q: files %+v", text, files)
		}
	}
}

func TestShotPrefixTakesBothSeparatorsOnWindowsOnly(t *testing.T) {
	win := regexp.MustCompile("^" + shotPrefix(`D:\a]b\inbox`, true) + "$")
	for _, s := range []string{`D:\a]b\inbox\`, `D:\a]b\inbox/`, `D:/a]b/inbox/`} {
		if !win.MatchString(s) {
			t.Errorf("windows prefix does not take %q", s)
		}
	}
	for _, s := range []string{`D:\a]b\inboxx\`, `E:\a]b\inbox\`, `D:\a]b\inbox`} {
		if win.MatchString(s) {
			t.Errorf("windows prefix takes %q", s)
		}
	}
	unix := regexp.MustCompile("^" + shotPrefix(`/h/a\b/inbox`, false) + "$")
	if !unix.MatchString(`/h/a\b/inbox/`) || unix.MatchString(`/h/a/b/inbox/`) {
		t.Error("unix prefix must keep a backslash in the path literal")
	}
}

func TestSizesReadInPlainUnits(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1 KiB", 312320: "305 KiB", 1048063: "1023 KiB", 1048064: "1.0 MiB", 3 << 20: "3.0 MiB"} {
		if got := size(n); got != want {
			t.Errorf("size(%d) = %q, want %q", n, got, want)
		}
	}
}
