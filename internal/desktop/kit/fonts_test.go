package kit

import (
	"bytes"
	"encoding/binary"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestTheEmbeddedFontsAreRegistered(t *testing.T) {
	if fontsRegistered != nil {
		t.Fatal(fontsRegistered)
	}
	got := map[string]bool{}
	for _, f := range ui.FontFamilies() {
		if f.Registered {
			got[f.Name] = f.Monospaced
		}
	}
	if mono, ok := got[Sans]; !ok || mono {
		t.Errorf("%s: registered %v, monospaced %v, want registered and proportional", Sans, ok, mono)
	}
	if mono, ok := got[Mono]; !ok || !mono {
		t.Errorf("%s: registered %v, monospaced %v, want registered and monospaced", Mono, ok, mono)
	}
}

func TestEachEmbeddedFontShipsItsLicence(t *testing.T) {
	for file, holder := range map[string]string{
		"fonts/OFL-NotoSans.txt":      "The Noto Project Authors",
		"fonts/OFL-JetBrainsMono.txt": "The JetBrains Mono Project Authors",
	} {
		b, err := fontFiles.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(b, []byte(holder)) || !strings.Contains(string(b), "SIL OPEN FONT LICENSE Version 1.1") {
			t.Errorf("%s does not carry %q and the SIL Open Font License 1.1", file, holder)
		}
	}
}

func TestTheFontsKeepTheirFaces(t *testing.T) {
	var regular, bold float32
	ui.NewTester(func(c *ui.Context) {
		regular, _ = c.MeasureText(1e6, ui.Span{Text: "Hand", Font: Sans, Size: Body.Size})
		bold, _ = c.MeasureText(1e6, ui.Span{Text: "Hand", Font: Sans, Size: Body.Size, Weight: 700})
	}, 200, 200)
	if bold <= regular {
		t.Errorf("bold Hand is %.1f wide and regular %.1f: the bold face is not drawn", bold, regular)
	}
}

func sfntTable(t *testing.T, data []byte, tag string) []byte {
	t.Helper()
	n := int(binary.BigEndian.Uint16(data[4:]))
	for i := 0; i < n; i++ {
		e := data[12+16*i:]
		if string(e[:4]) == tag {
			off, ln := binary.BigEndian.Uint32(e[8:]), binary.BigEndian.Uint32(e[12:])
			return data[off : off+ln]
		}
	}
	t.Fatalf("no %s table", tag)
	return nil
}

func utf16BE(s string) []byte {
	var b []byte
	for _, r := range s {
		b = append(b, 0, byte(r))
	}
	return b
}

func TestEachFontFileIsItsFamilyAndWeight(t *testing.T) {
	if Sans != "Noto Sans" || Mono != "JetBrains Mono" {
		t.Errorf("the families are %q and %q, want Noto Sans and JetBrains Mono", Sans, Mono)
	}
	want := map[string][]uint16{}
	for _, f := range fontList {
		data, err := fontFiles.ReadFile("fonts/" + f.file)
		if err != nil {
			t.Fatal(err)
		}
		weight := binary.BigEndian.Uint16(sfntTable(t, data, "OS/2")[4:])
		want[f.family] = append(want[f.family], weight)
		names := sfntTable(t, data, "name")
		if !bytes.Contains(names, []byte(f.family)) && !bytes.Contains(names, utf16BE(f.family)) {
			t.Errorf("%s does not carry the family name %q", f.file, f.family)
		}
	}
	for _, family := range []string{Sans, Mono} {
		if got := want[family]; len(got) != 2 || got[0] != 400 || got[1] != 600 {
			t.Errorf("%s registers weights %v, want Regular 400 and SemiBold 600", family, got)
		}
	}
}

func TestNoViewNamesAFontByLiteral(t *testing.T) {
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(src, []byte(`"monospace"`)) {
			t.Errorf("%s names \"monospace\"; use kit.Mono", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
