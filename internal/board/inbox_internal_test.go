package board

import (
	"slices"
	"strings"
	"testing"
)

func TestShotsTakeOnlyThisFleetsInbox(t *testing.T) {
	text := "look\n[image: /f/inbox/20261001-120000-1.png]\n[image: /g/inbox/20261001-120000-2.png]\n[image: /etc/x.png]"
	rest, names := shots(text, "/f")
	if rest != "look\n[image: /g/inbox/20261001-120000-2.png]\n[image: /etc/x.png]" || !slices.Equal(names, []string{"20261001-120000-1.png"}) {
		t.Fatalf("rest %q names %q", rest, names)
	}
	if rest, names := shots("[image: /f/inbox/20261001-120000-1.png]\n", "/f"); rest != "" || len(names) != 1 {
		t.Fatalf("only an image: rest %q names %q", rest, names)
	}
}

func TestSafeNameKeepsAPlainBaseName(t *testing.T) {
	for in, want := range map[string]string{
		"report.pdf":            "report.pdf",
		"../../etc/passwd":      "passwd",
		`..\..\win.ini`:         "win.ini",
		`C:\Users\me\notes.txt`: "notes.txt",
		".env":                  "env",
		"--rf":                  "rf",
		"..":                    "file",
		"":                      "file",
		"Q3 export (final).csv": "Q3_export_final_.csv",
		"résumé.pdf":            "r_sum_.pdf",
		"日本.txt":                "_.txt",
		"a\x00b\n.txt":          "a_b_.txt",
		"trailing.":             "trailing",
	} {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestALongNameKeepsItsExtension(t *testing.T) {
	long := clipName(strings.Repeat("a", 200) + ".pdf")
	if len(long) != maxName || !strings.HasSuffix(long, "a.pdf") {
		t.Fatalf("long = %q (%d)", long, len(long))
	}
	odd := clipName("a." + strings.Repeat("b", 200))
	if len(odd) != maxName {
		t.Fatalf("odd = %q (%d)", odd, len(odd))
	}
}
