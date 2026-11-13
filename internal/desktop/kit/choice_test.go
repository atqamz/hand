package kit

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func choices(picked string) []Choice {
	return []Choice{
		{Value: "1. Pin 0.14.2", Key: "1", Label: "Pin 0.14.2", Note: "reproducible builds; we bump it by hand", Recommended: true, Picked: picked == "1. Pin 0.14.2"},
		{Value: "2. Track 0.14.x", Key: "2", Label: "Track 0.14.x", Note: "patch fixes arrive by themselves", Picked: picked == "2. Track 0.14.x"},
	}
}

func TestTheChoiceListShowsTheNumberOfItsOptionsAndTheRecommendation(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ChoiceList(c, ColorsFor(false), choices(""))
	}, 600, 300)
	for _, want := range []string{"Pin 0.14.2", "1", "Track 0.14.x", "2", "reproducible builds; we bump it by hand", "Recommended"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	first, ok := tt.Find("1")
	if !ok {
		t.Fatalf("no first row in %q", tt.Texts())
	}
	second, ok := tt.Find("2")
	if !ok {
		t.Fatalf("no second row in %q", tt.Texts())
	}
	if second.Y-first.Y < 48 {
		t.Errorf("the rows are %v apart, want at least 48", second.Y-first.Y)
	}
}

func TestAPickedOptionFillsItsNumberCircle(t *testing.T) {
	for _, dark := range []bool{false, true} {
		k := ColorsFor(dark)
		for _, picked := range []string{"1. Pin 0.14.2", "2. Track 0.14.x"} {
			tt := ui.NewTester(func(c *ui.Context) {
				ChoiceList(c, k, choices(picked))
			}, 600, 300)
			row, ok := tt.Find("1")
			if !ok {
				t.Fatalf("no first row in %q", tt.Texts())
			}
			got := tt.Image().RGBAAt(int(row.X+9), int(row.Y+9))
			want := k.BG
			if picked == "1. Pin 0.14.2" {
				want = k.Accent
			}
			if got.R != want.R || got.G != want.G || got.B != want.B {
				t.Errorf("dark=%v picked=%q: circle is %v, want %v", dark, picked, got, want)
			}
		}
	}
}

func TestAChoiceIsPickedByClickAndNotWhileDisabled(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		var got string
		tt := ui.NewTester(func(c *ui.Context) {
			opts := choices("")
			for i := range opts {
				opts[i].Disabled = disabled
			}
			if picked := ChoiceList(c, ColorsFor(false), opts); picked != "" {
				got = picked
			}
		}, 600, 300)
		if err := tt.Click("Track 0.14.x"); err != nil {
			t.Fatal(err)
		}
		if disabled && got != "" {
			t.Errorf("a disabled list picked %q", got)
		}
		if !disabled && got != "2. Track 0.14.x" {
			t.Errorf("picked %q, want the second option", got)
		}
	}
}
