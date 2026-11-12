package desktop_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

func composerBox(t *testing.T, tt *ui.Tester) ui.Rect {
	t.Helper()
	box, ok := tt.Find("Composer")
	if !ok || box.W <= 0 || box.H <= 0 {
		t.Fatal("the composer box is not drawn")
	}
	return box
}

func TestScenesTheComposerAsTheBoardDrawsIt(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		t.Run(theme, func(t *testing.T) {
			tt := sized(t, newApp(t, goldenFixture(t)), 1100, 760, dark)
			mustText(t, tt, "Message", "Ctrl Enter", "Send")
			mustNotText(t, tt, "Clear")
			box := composerBox(t, tt)
			if box.W > 760 {
				t.Errorf("the empty box is %.0f wide, want at most 760", box.W)
			}
			if bottom := box.Y + box.H; bottom > 760 || 760-bottom > 20 {
				t.Errorf("the box ends at %.0f in a 760 px window", bottom)
			}
			shotSized(t, tt, "27-composer-empty-"+theme, 1100, 760)
		})
	}
}

func TestScenesTheComposerHint(t *testing.T) {
	const hint = "No supervisor is running; your message waits."
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		t.Run(theme, func(t *testing.T) {
			f := goldenFixture(t)
			st := f.status[fleetID]
			st.ComposerHint = hint
			f.status[fleetID] = st
			tt := sized(t, newApp(t, f), 1100, 760, dark)
			mustText(t, tt, hint, "Ctrl Enter", "Send")
			shotSized(t, tt, "28-composer-hint-"+theme, 1100, 760)
		})
	}
}

func TestScenesTheComposerDraftNote(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		t.Run(theme, func(t *testing.T) {
			app := newApp(t, goldenFixture(t))
			app.Now = func() time.Time { return time.Date(2026, 11, 1, 14, 32, 0, 0, time.UTC) }
			tt := sized(t, app, 1100, 760, dark)
			click(t, tt, "Message the supervisor")
			tt.Type("half a message")
			tt.Frame()
			mustText(t, tt, "Draft saved 14:32")
			shotSized(t, tt, "29-composer-draft-"+theme, 1100, 760)
			for range len("half a message") {
				tt.Key(0, ui.KeyBackspace)
			}
			tt.Frame()
			mustNotText(t, tt, "Draft saved")
			if app.Draft != "" {
				t.Fatalf("the box holds %q, want it empty", app.Draft)
			}
		})
	}
}

func TestScenesTheComposerSendsOnceFromCtrlEnterAndOnceFromSend(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		t.Run(theme, func(t *testing.T) {
			f := goldenFixture(t)
			tt := sized(t, newApp(t, f), 1100, 760, dark)
			composerBox(t, tt)
			click(t, tt, "Message the supervisor")
			tt.Type("hello there")
			tt.Key(ui.Ctrl, ui.KeyEnter)
			tt.Frame()
			if got := f.count("send:" + fleetID); got != 1 || f.sentText != "hello there" {
				t.Fatalf("Ctrl+Enter sent %d times, %q", got, f.sentText)
			}
			mustText(t, tt, "hello there")
			click(t, tt, "Message the supervisor")
			tt.Type("again")
			click(t, tt, "Send")
			tt.Frame()
			if got := f.count("send:" + fleetID); got != 2 || f.sentText != "again" {
				t.Fatalf("Send sent %d times, %q", got, f.sentText)
			}
			shotSized(t, tt, "30-composer-sent-"+theme, 1100, 760)
		})
	}
}

func TestScenesTheComposerKeepsAFailedSend(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		t.Run(theme, func(t *testing.T) {
			f := goldenFixture(t)
			app := newApp(t, f)
			app.Now = func() time.Time { return time.Date(2026, 11, 1, 14, 32, 0, 0, time.UTC) }
			tt := sized(t, app, 1100, 760, dark)
			click(t, tt, "Message the supervisor")
			tt.Type("keep this text")
			f.action = &desktop.APIError{Status: 500, Code: "internal", Message: "the supervisor is gone"}
			click(t, tt, "Send")
			tt.Frame()
			mustText(t, tt, "The action did not go through", "the supervisor is gone")
			if app.Draft != "keep this text" {
				t.Fatalf("draft = %q after a failed send", app.Draft)
			}
			box := composerBox(t, tt)
			if box.W > 760 {
				t.Errorf("the box is %.0f wide, want at most 760", box.W)
			}
			shotSized(t, tt, "31-composer-failed-"+theme, 1100, 760)
		})
	}
}

func TestTheComposerHoldsTheBoardsWidth(t *testing.T) {
	for _, width := range []int{800, 900, 1100, 1440, 2400} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			app := newApp(t, goldenFixture(t))
			app.Chat = 1200
			tt := sized(t, app, width, 760, false)
			drawn := drawnZoom(1, width)
			box := composerBox(t, tt)
			if box.W/drawn > 760.5 {
				t.Errorf("%d wide: the box is %.0f logical, want at most 760", width, box.W/drawn)
			}
			if bottom := (760 - (box.Y + box.H)) / drawn; box.Y+box.H > 760 || bottom < 15 {
				t.Errorf("%d wide: the box ends %.0f logical px above the bottom, want 16", width, bottom)
			}
			head, ok := tt.Find("Supervisor")
			if !ok {
				t.Fatal("no pane head")
			}
			left, right := (box.X-head.X)/drawn, (head.X+head.W-(box.X+box.W))/drawn
			if left < 15 {
				t.Errorf("%d wide: the box starts %.0f logical px into the pane, want 16", width, left)
			}
			if diff := right - left; diff > 1 || diff < -1 {
				t.Errorf("%d wide: the box has %.0f logical px on its left and %.0f on its right, want it centred", width, left, right)
			}
		})
	}
}
