package desktop_test

import (
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/kit"
	"github.com/egoist/mygo/ui"
)

func booted(t *testing.T, drafts string) *desktop.App {
	t.Helper()
	f := goldenFixture(t)
	app := desktop.New(func() (desktop.ReadModel, error) { return f, nil })
	app.DraftDir = drafts
	app.Boot()
	if app.Err != "" {
		t.Fatalf("boot: %s", app.Err)
	}
	return app
}

func TestTheZoomKeysStepTheLadder(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	for _, step := range []struct {
		mods ui.Modifiers
		key  ui.Key
		want float32
	}{
		{ui.Ctrl, ui.KeyEqual, 1.1},
		{ui.Ctrl | ui.Shift, ui.KeyEqual, 1.25},
		{ui.Ctrl, ui.KeyEqual, 1.5},
		{ui.Ctrl, ui.KeyMinus, 1.25},
		{ui.Ctrl, ui.KeyMinus, 1.1},
		{ui.Ctrl, ui.Key0, 1},
	} {
		tt.Key(step.mods, step.key)
		if math.Abs(float64(app.Zoom-step.want)) > 1e-6 {
			t.Fatalf("after Ctrl+%v zoom is %v, want %v", step.key, app.Zoom, step.want)
		}
		rail, ok := tt.Find("Pages and fleets")
		if !ok || math.Abs(float64(rail.W/step.want)-kit.RailWidth) > 0.01 {
			t.Fatalf("the rail is %v at zoom %v, want %d logical px", rail.W, app.Zoom, kit.RailWidth)
		}
	}
}

func TestTheZoomLadderClampsAtBothEnds(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	app.Zoom = 5
	tt.Frame()
	tt.Key(ui.Ctrl, ui.KeyEqual)
	if app.Zoom != 5 {
		t.Fatalf("zooming in at 500%% made %v", app.Zoom)
	}
	for app.Zoom > .25 {
		tt.Key(ui.Ctrl, ui.KeyMinus)
	}
	tt.Key(ui.Ctrl, ui.KeyMinus)
	if app.Zoom != .25 {
		t.Fatalf("zooming out at 25%% made %v", app.Zoom)
	}
	tt.Key(ui.Ctrl, ui.Key0)
	if app.Zoom != 1 {
		t.Fatalf("reset made %v, want 1", app.Zoom)
	}
}

func TestTheZoomSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	app := booted(t, dir)
	tt := newTester(app)
	tt.Key(ui.Ctrl, ui.KeyEqual)
	tt.Key(ui.Ctrl, ui.KeyEqual)
	if app.Zoom != 1.25 {
		t.Fatalf("zoom is %v, want 1.25", app.Zoom)
	}
	if _, err := os.Stat(desktop.PrefsPath(dir)); err != nil {
		t.Fatalf("no preferences file: %v", err)
	}
	again := booted(t, dir)
	if again.Zoom != app.Zoom {
		t.Fatalf("the restarted window boots at %v, want %v", again.Zoom, app.Zoom)
	}
}

func TestAMalformedPreferencesFileBootsAt100(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(desktop.PrefsPath(dir), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if app := booted(t, dir); app.Zoom != 1 {
		t.Fatalf("a malformed preferences file boots at %v, want 1", app.Zoom)
	}
}

func composerRect(t *testing.T, tt *ui.Tester) ui.Rect {
	t.Helper()
	for _, label := range []string{"Message", "Message the supervisor"} {
		if r, ok := tt.Find(label); ok {
			return r
		}
	}
	t.Fatal("no composer")
	return ui.Rect{}
}

func TestAZoomedWindowKeepsTheBoardsLayout(t *testing.T) {
	for _, tc := range []struct {
		zoom  float32
		scene int
	}{{1.25, 27}, {1.5, 28}} {
		zoom := tc.zoom
		for _, dark := range []bool{false, true} {
			theme := "light"
			if dark {
				theme = "dark"
			}
			t.Run(fmt.Sprintf("%.0f%%-%s", zoom*100, theme), func(t *testing.T) {
				app := newApp(t, goldenFixture(t))
				tt := sized(t, app, windowWidth, windowHeight, dark)
				app.Zoom = zoom
				tt.Frame()
				mustText(t, tt, "Needs", "Tasks", "Supervisor", "More", "Fix login", "Message")

				rail, ok := tt.Find("Pages and fleets")
				if !ok || math.Abs(float64(rail.W/zoom)-kit.RailWidth) > .01 || rail.X != 0 {
					t.Fatalf("the rail is %v at zoom %v, want %d logical px from the left", rail, zoom, kit.RailWidth)
				}
				head, ok := tt.Find("Supervisor")
				if !ok || head.X <= rail.W || math.Abs(float64(head.X+head.W-float32(windowWidth))) > 1 {
					t.Fatalf("the chat pane is %v in a %d px window", head, windowWidth)
				}
				message := composerRect(t, tt)
				left, right := (message.X-head.X)/zoom, (float32(windowWidth)-message.X-message.W)/zoom
				if left < 16 || right < 16 {
					t.Fatalf("the composer has %v and %v logical px beside it, want at least 16", left, right)
				}
				if message.W/zoom > 760 {
					t.Fatalf("the composer is %v logical px wide, more than the Board's 760", message.W/zoom)
				}
				for _, s := range []string{"Needs", "Tasks", "Supervisor", "Send", "More", "Message"} {
					r, ok := tt.Find(s)
					if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > float32(windowWidth) || r.Y+r.H > float32(windowHeight) {
						t.Errorf("%q is at %v, outside the %dx%d window", s, r, windowWidth, windowHeight)
					}
				}
				shot(t, tt, fmt.Sprintf("%d-zoom-%.0f-%s", tc.scene, zoom*100, theme))

				if zoom != 1.25 {
					return
				}
				click(t, tt, "Message")
				tt.Type("The zoom keeps this line.")
				tt.Frame()
				if app.Draft != "The zoom keeps this line." {
					t.Fatalf("the typed draft is %q", app.Draft)
				}
				caret, ok := tt.TextCaret()
				if !ok || caret.X < message.X || caret.X+caret.W > message.X+message.W ||
					caret.Y < message.Y || caret.Y+caret.H > message.Y+message.H {
					t.Fatalf("the caret is at %v, outside the composer %v", caret, message)
				}
			})
		}
	}
}
