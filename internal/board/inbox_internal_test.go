package board

import (
	"os"
	"path/filepath"
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
	rest, files := shots(text, home)
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
	if rest, files := shots("[image: "+filepath.Join(inbox, "20261001-120000-1.png")+"]\n", home); rest != "" || len(files) != 1 {
		t.Fatalf("only an image: rest %q files %+v", rest, files)
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
	if dot := clipName(strings.Repeat("c", 79) + "." + strings.Repeat("d", 30)); dot != strings.Repeat("c", 79) {
		t.Fatalf("a clip that ends on a dot = %q", dot)
	}
}

func TestSizesReadInPlainUnits(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1 KiB", 312320: "305 KiB", 1048063: "1023 KiB", 1048064: "1.0 MiB", 3 << 20: "3.0 MiB"} {
		if got := size(n); got != want {
			t.Errorf("size(%d) = %q, want %q", n, got, want)
		}
	}
}
