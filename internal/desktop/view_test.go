package desktop_test

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/egoist/mygo/ui"
)

const (
	windowWidth  = 1100
	windowHeight = 760
)

func newTester(app *desktop.App) *ui.Tester {
	return ui.NewTester(app.View, windowWidth, windowHeight)
}

func shot(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	img := tt.Image()
	if img == nil {
		t.Fatal("no frame")
	}
	if img.Bounds().Dx() != windowWidth || img.Bounds().Dy() != windowHeight {
		t.Fatalf("frame is %v, want %dx%d", img.Bounds(), windowWidth, windowHeight)
	}
	dir := os.Getenv("DESKTOP_SHOTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func mustText(t *testing.T, tt *ui.Tester, want ...string) {
	t.Helper()
	for _, s := range want {
		if !tt.HasText(s) {
			t.Errorf("missing %q in %q", s, tt.Texts())
		}
	}
}

func mustNotText(t *testing.T, tt *ui.Tester, want string) {
	t.Helper()
	if tt.HasText(want) {
		t.Errorf("unexpected %q in %q", want, tt.Texts())
	}
}

func click(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	if err := tt.Click(text); err != nil {
		t.Fatalf("click %q: %v", text, err)
	}
}

func TestFleetSwitch(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	mustText(t, tt, "golden", "second")
	click(t, tt, "second")
	mustText(t, tt, "the second fleet's status", "second")
	if app.Fleet.ID != secondID {
		t.Fatalf("fleet = %s, want %s", app.Fleet.ID, secondID)
	}
}

func TestStatusScreen(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	mustText(t, tt, "Status", "Supervisor", "s1 claude sonnet low · running",
		"Active", "Inbox", "Done", "Abandoned", "Projects", "agent working")
}

func TestNeedsScreen(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Needs")
	mustText(t, tt, "Needs", "FAILED", "DECISION", "REPORT",
		"Keep the old cookie name?",
		"Flaky test in the nightly pipeline on the arm64 macOS runner after the dependency bump",
		"Cookie fixed")
}

func TestNeedsOpensADecision(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Needs")
	click(t, tt, "Keep the old cookie name?")
	mustText(t, tt, "d1", "Options", "Yes, keep it", "No, rename it", "Open task")
}

func TestTimelineScreen(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Timeline")
	mustText(t, tt, "Timeline", "s1 is working", "All good.", "status?", "you", "hand")
}

func TestTasksScreenAndDetail(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Tasks")
	mustText(t, tt, "Tasks", "Fix login", "Write docs", "Active", "Inbox")
	click(t, tt, "Fix login")
	mustText(t, tt, "Fix login", "1. reproduce", "Plan · p1", "Cookie fixed",
		"Keep the old cookie name?", "a1")
	click(t, tt, "Back")
	mustText(t, tt, "Write docs")
}

func TestAttemptScreen(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Tasks")
	click(t, tt, "Fix login")
	click(t, tt, "a1")
	mustText(t, tt, "Brief", "x", "20m", "Cookie fixed", "Reports")
}

func TestReportScreen(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	click(t, tt, "Needs")
	click(t, tt, "Cookie fixed")
	mustText(t, tt, "r1", "Cookie fixed", "All tests pass.", "Open task")
}

func TestEmptyStates(t *testing.T) {
	app := newApp(t, emptyFixture())
	tt := newTester(app)
	click(t, tt, "Needs")
	mustText(t, tt, "All clear", "Nothing waits on you.")
	click(t, tt, "Timeline")
	mustText(t, tt, "No messages")
	click(t, tt, "Tasks")
	mustText(t, tt, "No tasks")
}

func TestErrorState(t *testing.T) {
	app := newApp(t, errorFixture())
	tt := newTester(app)
	mustText(t, tt, "The board could not be read",
		"the board did not accept this fleet's token; open the fleet once with `hand open`")
	click(t, tt, "Retry")
	mustText(t, tt, "The board could not be read")
}

func TestScenes(t *testing.T) {
	type scene struct {
		name string
		app  *desktop.App
		play func(*testing.T, *ui.Tester)
	}
	scenes := []scene{
		{"01-fleet-switch", newApp(t, goldenFixture(t)), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "second")
		}},
		{"02-status", newApp(t, goldenFixture(t)), nil},
		{"03-needs", newApp(t, goldenFixture(t)), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Needs")
		}},
		{"04-timeline", newApp(t, goldenFixture(t)), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Timeline")
		}},
		{"05-tasks", newApp(t, goldenFixture(t)), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Tasks")
		}},
		{"06-detail", newApp(t, goldenFixture(t)), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Tasks")
			click(t, tt, "Fix login")
		}},
		{"07-empty", newApp(t, emptyFixture()), func(t *testing.T, tt *ui.Tester) {
			click(t, tt, "Needs")
		}},
		{"08-error", newApp(t, errorFixture()), nil},
	}
	for _, sc := range scenes {
		t.Run(sc.name, func(t *testing.T) {
			tt := newTester(sc.app)
			if sc.play != nil {
				sc.play(t, tt)
			}
			shot(t, tt, sc.name)
		})
	}
}

func TestFrameIsNotBlank(t *testing.T) {
	app := newApp(t, goldenFixture(t))
	tt := newTester(app)
	img := tt.Image()
	bg := img.RGBAAt(0, 0)
	varied := 0
	for y := 0; y < img.Bounds().Dy(); y += 4 {
		for x := 0; x < img.Bounds().Dx(); x += 4 {
			if img.RGBAAt(x, y) != bg {
				varied++
			}
		}
	}
	if varied < 100 {
		t.Fatalf("the frame is blank: %d sampled pixels differ from %v", varied, bg)
	}
}

func TestBootWithoutABoard(t *testing.T) {
	app := desktop.New(func() (desktop.ReadModel, error) { return nil, desktop.ErrNoBoard })
	app.Boot()
	tt := newTester(app)
	mustText(t, tt, "The board could not be read", "no board is running; start it with `hand open` in a fleet")
	click(t, tt, "Retry")
	mustText(t, tt, "The board could not be read")
}
