package kit

import (
	"bytes"
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
