package desktop_test

import (
	"context"
	"errors"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
)

const tasksFailed = "the tasks read failed"

func TestScenesTasksOnlyFailureDrawsTheErrorCard(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
		dark          bool
	}{{"wide-light", 1100, 760, false}, {"wide-dark", 1100, 760, true}, {"narrow-light", 880, 760, false}, {"narrow-dark", 880, 760, true}} {
		t.Run(tc.name, func(t *testing.T) {
			f := goldenFixture(t)
			app := newApp(t, f)
			tt := sized(t, app, tc.width, tc.height, tc.dark)
			if tc.width < 1000 {
				click(t, tt, "Tasks")
			}
			tt.Frame()
			mustNotText(t, tt, "The board could not be read")
			f.tasksErr = errors.New(tasksFailed)
			app.Retry()
			tt.Frame()
			mustText(t, tt, "The board could not be read", tasksFailed, "Retry")
			if n := countText(t, tt, "The board could not be read"); n != 1 {
				t.Fatalf("%d error cards, want 1", n)
			}
			if tc.width >= 1000 {
				mustText(t, tt, "Needs", "Keep the old cookie name?", "Message")
				mustNotText(t, tt, "No tasks")
			}
			if tc.width == windowWidth {
				shot(t, tt, "tasks-error-"+tc.name)
			}
			if tc.width < 1000 {
				for _, tab := range []string{"Needs", "Chat"} {
					click(t, tt, tab)
					tt.Frame()
					mustNotText(t, tt, "The board could not be read")
					mustNotText(t, tt, tasksFailed)
					mustNotText(t, tt, "Retry")
				}
				click(t, tt, "Tasks")
				tt.Frame()
			}
			f.tasksErr = nil
			click(t, tt, "Retry")
			tt.Frame()
			mustNotText(t, tt, "The board could not be read")
			mustNotText(t, tt, tasksFailed)
		})
	}
}

type screenFailPoll struct {
	desktop.ReadModel
	err error
}

func (p *screenFailPoll) Poll(ctx context.Context, id string, s desktop.Screen, ref string, c desktop.PollCursors) (desktop.Poll, error) {
	if p.err == nil {
		return p.ReadModel.Poll(ctx, id, s, ref, c)
	}
	return desktop.Poll{StatusFresh: true, StatusRead: true, ScreenErr: p.err, TimelineRead: true}, nil
}

func TestATasksOnlyPollFailureDrawsTheCardAndRecovers(t *testing.T) {
	f := goldenFixture(t)
	model := &screenFailPoll{ReadModel: f}
	app := desktop.New(func() (desktop.ReadModel, error) { return model, nil })
	app.DraftDir = t.TempDir()
	app.Boot()
	clock := newFakeClock()
	app.Clock = clock
	tt := sized(t, app, 1100, 760, false)
	model.err = errors.New(tasksFailed)
	app.StartRefresh()
	clock.Advance(desktop.FocusedRefresh)
	tt.Frame()
	mustText(t, tt, "Stale · retrying", "The board could not be read", tasksFailed, "Retry", "Needs")
	model.err = nil
	click(t, tt, "Retry")
	tt.Frame()
	mustNotText(t, tt, "The board could not be read")
	mustText(t, tt, "Needs")
}

func TestAStatusFailureStillDrawsOneBoardCard(t *testing.T) {
	f := goldenFixture(t)
	f.fail = &desktop.APIError{Status: 503, Code: "unavailable", Message: "the board is restarting"}
	app := newApp(t, f)
	tt := sized(t, app, 1100, 760, false)
	tt.Frame()
	mustText(t, tt, "The board could not be read", "the board is restarting")
	if n := countText(t, tt, "The board could not be read"); n != 1 {
		t.Fatalf("%d error cards, want 1", n)
	}
}
