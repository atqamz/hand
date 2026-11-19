package desktop_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
)

const initOut = "home: x\nfleet: made\nid: f0000000000aa\ncreated: true\nluvus: pinned 0.14.3 (x)\n"

func getenvFor(root, home string) func(string) string {
	return func(k string) string {
		switch k {
		case "SECONDHAND_HOME":
			return root
		case "HOME", "USERPROFILE":
			return home
		}
		return ""
	}
}

func resolved(t *testing.T, dir string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func pairs(t *testing.T, h fakeHand) [][2]string {
	t.Helper()
	calls := h.calls(t)
	if len(calls)%2 != 0 {
		t.Fatalf("odd call log %q", calls)
	}
	var out [][2]string
	for i := 0; i < len(calls); i += 2 {
		out = append(out, [2]string{calls[i], calls[i+1]})
	}
	return out
}

func TestNewFleetChecksTheFormBeforeAnyExec(t *testing.T) {
	hand := installFakeHand(t)
	home := resolved(t, t.TempDir())
	getenv := getenvFor(t.TempDir(), home)
	notEmpty := filepath.Join(home, "full")
	if err := os.MkdirAll(notEmpty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notEmpty, "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	outer := filepath.Join(home, "outer")
	if err := os.MkdirAll(outer, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outer, "hand.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fleets := []desktop.Fleet{{ID: "f1", Name: "taken"}}
	fresh := filepath.Join(home, "fresh")
	cases := []struct {
		name, req, folder, want string
	}{
		{"empty name", "", fresh, "Name is required."},
		{"blank name", "   ", fresh, "Name is required."},
		{"long name", strings.Repeat("a", 65), fresh, "1 to 64 bytes"},
		{"control name", "a\nb", fresh, "1 to 64 bytes"},
		{"duplicate name", "taken", fresh, `A fleet named "taken" already exists`},
		{"relative folder", "new", "fleets/new", "absolute"},
		{"dot folder", "new", ".", "absolute"},
		{"empty folder", "new", "", "absolute"},
		{"tilde user", "new", "~other/x", "absolute"},
		{"folder is a file", "new", file, "is a file"},
		{"folder not empty", "new", notEmpty, "not empty"},
		{"home not empty", "new", "~", "not empty"},
		{"inside a fleet", "new", filepath.Join(outer, "a", "b"), "inside the fleet at " + outer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := desktop.NewFleet(getenv)(context.Background(), desktop.NewFleetRequest{Name: tc.req, Folder: tc.folder, Fleets: fleets})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if res.ID != "" || res.Cmd != "" {
				t.Fatalf("result = %+v", res)
			}
			if got := hand.calls(t); len(got) != 0 {
				t.Fatalf("hand ran: %q", got)
			}
		})
	}
}

func TestNewFleetAcceptsAnEmptyFolder(t *testing.T) {
	installFakeHand(t)
	t.Setenv("FAKE_HAND_OUT", initOut)
	home := resolved(t, t.TempDir())
	empty := filepath.Join(home, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := desktop.NewFleet(getenvFor(t.TempDir(), home))(context.Background(), desktop.NewFleetRequest{Name: "new", Folder: empty}); err != nil {
		t.Fatal(err)
	}
}

func TestNewFleetRunsInitThenBoardStartEvenWhenABoardAnswers(t *testing.T) {
	hand := installFakeHand(t)
	t.Setenv("FAKE_HAND_OUT", initOut)
	argvLog := filepath.Join(t.TempDir(), "argv")
	t.Setenv("FAKE_HAND_ARGV", argvLog)
	root := t.TempDir()
	if err := writeAddr(root, newAnsweringServer(t)); err != nil {
		t.Fatal(err)
	}
	home := resolved(t, t.TempDir())
	dir := filepath.Join(home, "my fleets", "a b")
	res, err := desktop.NewFleet(getenvFor(root, home))(context.Background(), desktop.NewFleetRequest{Name: "-odd; touch x", Folder: "~/my fleets/a b"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "f0000000000aa" || res.Folder != dir || res.Warn != "" {
		t.Fatalf("result = %+v", res)
	}
	b, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	wantArgv := `["init","--name","-odd; touch x",` + strconv.Quote(dir) + "]\n" + `["--home",` + strconv.Quote(dir) + `,"board","start"]` + "\n"
	if string(b) != wantArgv {
		t.Fatalf("argv = %s, want %s", b, wantArgv)
	}
	got := pairs(t, hand)
	if len(got) != 2 {
		t.Fatalf("hand ran %q, want init then board start", got)
	}
	if want := "init --name -odd; touch x " + dir; got[0][1] != want {
		t.Fatalf("init ran with %q, want %q", got[0][1], want)
	}
	if want := "--home " + dir + " board start"; got[1][1] != want {
		t.Fatalf("board start ran with %q, want %q", got[1][1], want)
	}
	if wd, err := filepath.EvalSymlinks(got[1][0]); err != nil || wd != dir {
		t.Fatalf("board start ran in %q, want %q", got[1][0], dir)
	}
	if _, err := os.Stat(filepath.Join(home, "touch")); err == nil {
		t.Fatal("the name reached a shell")
	}
}

func TestNewFleetFailureLines(t *testing.T) {
	hand := installFakeHand(t)
	home := resolved(t, t.TempDir())
	getenv := getenvFor(t.TempDir(), home)
	dir := filepath.Join(home, "new fleet")
	req := desktop.NewFleetRequest{Name: "new", Folder: dir}
	run := func() (desktop.NewFleetResult, error) {
		return desktop.NewFleet(getenv)(context.Background(), req)
	}

	t.Setenv("FAKE_HAND_FAIL", "error: conflict: x is inside the fleet at y; make the fleet somewhere else")
	res, err := run()
	if err == nil || err.Error() != "error: conflict: x is inside the fleet at y; make the fleet somewhere else" {
		t.Fatalf("init refused: %v", err)
	}
	if res.ID != "" || res.Cmd != "hand init --name new '"+dir+"'" && runtime.GOOS != "windows" {
		t.Fatalf("result = %+v", res)
	}

	t.Setenv("FAKE_HAND_FAIL", "")
	t.Setenv("FAKE_HAND_OUT", initOut)
	t.Setenv("FAKE_HAND_FAIL_VERB", "board")
	t.Setenv("FAKE_HAND_FAIL", "hand board: listen tcp 127.0.0.1:7777: bind: address already in use")
	res, err = run()
	if err == nil || !strings.HasPrefix(err.Error(), "The fleet was made, but its Board did not start: ") ||
		!strings.HasSuffix(err.Error(), "address already in use") {
		t.Fatalf("board start failed: %v", err)
	}
	if res.ID != "f0000000000aa" || !strings.HasSuffix(res.Cmd, " board start") {
		t.Fatalf("result = %+v", res)
	}
	hand.reset(t)

	t.Setenv("FAKE_HAND_FAIL", "")
	req.Made = &res
	res2, err := run()
	if err != nil || res2.ID != res.ID {
		t.Fatalf("retry: %+v %v", res2, err)
	}
	got := pairs(t, hand)
	if len(got) != 1 || !strings.HasSuffix(got[0][1], "board start") {
		t.Fatalf("retry ran %q, want only board start", got)
	}
	hand.reset(t)

	req.Made = nil
	req.Folder = filepath.Join(home, "slow")
	t.Setenv("FAKE_HAND_FAIL_VERB", "")
	t.Setenv("FAKE_HAND_SLEEP", "1")
	defer desktop.SetInitTimeout(150 * time.Millisecond)()
	_, err = run()
	if err == nil || err.Error() != "`hand init` did not finish in time; the folder may be half-made" {
		t.Fatalf("timeout: %v", err)
	}
}

func TestNewFleetWithoutHand(t *testing.T) {
	t.Setenv("PATH", "")
	home := resolved(t, t.TempDir())
	res, err := desktop.NewFleet(getenvFor(t.TempDir(), home))(context.Background(), desktop.NewFleetRequest{Name: "new", Folder: filepath.Join(home, "n")})
	if err == nil || !strings.Contains(err.Error(), "`hand` was not found next to hand-desktop or on PATH") || res.Cmd != "" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestNewFleetWarnsWhenLuvusWasNotPinned(t *testing.T) {
	installFakeHand(t)
	t.Setenv("FAKE_HAND_OUT", "id: f0000000000aa\nhelp[1]:\n  - Install Luvus, then pin it: `hand luvus pin` (download failed: offline)\n")
	home := resolved(t, t.TempDir())
	res, err := desktop.NewFleet(getenvFor(t.TempDir(), home))(context.Background(), desktop.NewFleetRequest{Name: "new", Folder: filepath.Join(home, "n")})
	if err != nil || res.Warn != "Install Luvus, then pin it: `hand luvus pin` (download failed: offline)" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestNewFleetFailsWhenInitPrintsNoID(t *testing.T) {
	installFakeHand(t)
	t.Setenv("FAKE_HAND_OUT", "home: x\n")
	home := resolved(t, t.TempDir())
	_, err := desktop.NewFleet(getenvFor(t.TempDir(), home))(context.Background(), desktop.NewFleetRequest{Name: "new", Folder: filepath.Join(home, "n")})
	if err == nil || err.Error() != "`hand init` printed no fleet id" {
		t.Fatal(err)
	}
}

type newFleetSpy struct {
	reqs []desktop.NewFleetRequest
	res  []desktop.NewFleetResult
	errs []error
}

func (s *newFleetSpy) run(_ context.Context, req desktop.NewFleetRequest) (desktop.NewFleetResult, error) {
	i := len(s.reqs)
	s.reqs = append(s.reqs, req)
	return s.res[i], s.errs[i]
}

func zeroFleetApp(t *testing.T, connect func() (desktop.ReadModel, error), spy *newFleetSpy) (*desktop.App, *ui.Tester) {
	t.Helper()
	app := startApp(t, connect, (&startSpy{}).run)
	app.NewFleet = spy.run
	app.Post = nil
	tt := newTester(app)
	app.StartRefresh()
	tt.Frame()
	return app, tt
}

func noFleets() (desktop.ReadModel, error) { return noFleetsFixture(), nil }

func TestTheZeroFleetCardIsTheNewFleetForm(t *testing.T) {
	app, tt := zeroFleetApp(t, noFleets, &newFleetSpy{})
	mustText(t, tt, "No fleet on this computer yet.", "Name", "Folder", "Create fleet", "Look again")
	if _, folder := desktop.NewFleetForm(app); folder != "~/fleets/" {
		t.Fatalf("folder = %q", folder)
	}
	mustNotText(t, tt, "cd ~/work && hand init")
	mustNotText(t, tt, "Choose...")
}

func TestTheFolderFollowsTheNameUntilEdited(t *testing.T) {
	app, tt := zeroFleetApp(t, noFleets, &newFleetSpy{})
	click(t, tt, "Fleet name")
	tt.Type("a/b: c")
	tt.Frame()
	tt.Frame()
	if name, folder := desktop.NewFleetForm(app); name != "a/b: c" || folder != "~/fleets/a-b- c" {
		t.Fatalf("form = %q %q", name, folder)
	}
	click(t, tt, "Fleet folder")
	selectAll := ui.Ctrl
	if runtime.GOOS == "darwin" {
		selectAll = ui.Cmd
	}
	tt.Key(selectAll, ui.KeyA)
	tt.Type("/srv/mine")
	tt.Frame()
	click(t, tt, "Fleet name")
	tt.Type("d")
	tt.Frame()
	tt.Frame()
	if name, folder := desktop.NewFleetForm(app); name != "a/b: cd" || folder != "/srv/mine" {
		t.Fatalf("form = %q %q", name, folder)
	}
}

func TestCreateSendsTheFormAndOpensTheNewFleet(t *testing.T) {
	f := goldenFixture(t)
	made := false
	connect := func() (desktop.ReadModel, error) {
		if made {
			return f, nil
		}
		return noFleetsFixture(), nil
	}
	spy := &newFleetSpy{
		res:  []desktop.NewFleetResult{{ID: fleetID, Folder: "/x", Warn: "Install Luvus, then pin it"}},
		errs: []error{nil},
	}
	app, tt := zeroFleetApp(t, connect, spy)
	click(t, tt, "Fleet name")
	tt.Type("golden")
	tt.Frame()
	tt.Frame()
	made = true
	click(t, tt, "Create fleet")
	if len(spy.reqs) != 1 || spy.reqs[0].Name != "golden" || spy.reqs[0].Folder != "~/fleets/golden" || spy.reqs[0].Made != nil {
		t.Fatalf("requests = %+v", spy.reqs)
	}
	deadline := time.Now().Add(5 * time.Second)
	for app.Fleet.ID != fleetID && time.Now().Before(deadline) {
		tt.Frame()
		time.Sleep(5 * time.Millisecond)
	}
	if app.Fleet.ID != fleetID {
		t.Fatalf("selected %q, texts %q", app.Fleet.ID, tt.Texts())
	}
	tt.Frame()
	mustText(t, tt, "Fix login", "Install Luvus, then pin it")
	mustNotText(t, tt, "Create fleet")
}

func TestAFailedCreateKeepsTheFormAndShowsTheCommand(t *testing.T) {
	spy := &newFleetSpy{
		res: []desktop.NewFleetResult{
			{},
			{Cmd: "hand init --name new /x"},
			{ID: fleetID, Folder: "/x", Cmd: "hand --home /x board start"},
			{ID: fleetID, Folder: "/x"},
		},
		errs: []error{
			errors.New("That folder is not empty. Pick a new or empty folder."),
			errors.New("error: conflict: boom"),
			errors.New("The fleet was made, but its Board did not start: bind failed"),
			nil,
		},
	}
	app, tt := zeroFleetApp(t, noFleets, spy)
	click(t, tt, "Fleet name")
	tt.Type("new")
	tt.Frame()
	click(t, tt, "Create fleet")
	tt.Frame()
	mustText(t, tt, "That folder is not empty. Pick a new or empty folder.")
	mustNotText(t, tt, "Copy the command")

	click(t, tt, "Create fleet")
	tt.Frame()
	mustText(t, tt, "error: conflict: boom", "hand init --name new /x", "Copy the command")
	if name, folder := desktop.NewFleetForm(app); name != "new" || folder != "~/fleets/new" {
		t.Fatalf("form = %q %q", name, folder)
	}
	click(t, tt, "Copy the command")
	mustText(t, tt, "Copied")

	click(t, tt, "Create fleet")
	tt.Frame()
	mustText(t, tt, "The fleet was made, but its Board did not start: bind failed", "hand --home /x board start", "Start the Board")
	mustNotText(t, tt, "Create fleet")

	click(t, tt, "Start the Board")
	if len(spy.reqs) != 4 || spy.reqs[3].Made == nil || spy.reqs[3].Made.ID != fleetID || spy.reqs[2].Made != nil {
		t.Fatalf("requests = %+v", spy.reqs)
	}
}

func TestCreateKeepsTheWindowLive(t *testing.T) {
	release := make(chan struct{})
	app := startApp(t, noFleets, (&startSpy{}).run)
	app.NewFleet = func(ctx context.Context, req desktop.NewFleetRequest) (desktop.NewFleetResult, error) {
		<-release
		return desktop.NewFleetResult{}, errors.New("stopped")
	}
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	app.StartRefresh()
	tt.Frame()
	for settle := time.Now().Add(300 * time.Millisecond); time.Now().Before(settle); {
		select {
		case fn := <-posted:
			fn()
		case <-time.After(10 * time.Millisecond):
		}
	}
	tt.Frame()
	click(t, tt, "Create fleet")
	tt.Frame()
	mustText(t, tt, "Creating…")
	click(t, tt, "Creating…")
	close(release)
	waitPost(t, posted)()
	tt.Frame()
	mustText(t, tt, "stopped", "Create fleet")
}

func TestScenesNewFleet(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		t.Run(theme, func(t *testing.T) {
			spy := &newFleetSpy{
				res: []desktop.NewFleetResult{{}, {Cmd: "hand init --name atlas ~/fleets/atlas"}},
				errs: []error{
					errors.New("That folder is not empty. Pick a new or empty folder."),
					errors.New("error: conflict: /home/you/fleets/atlas is inside the fleet at /home/you/fleets; make the fleet somewhere else"),
				},
			}
			_, tt := zeroFleetApp(t, noFleets, spy)
			tt.SetDark(dark)
			click(t, tt, "Fleet name")
			tt.Type("atlas")
			tt.Frame()
			tt.Frame()
			mustText(t, tt, "No fleet on this computer yet.", "Create fleet")
			shot(t, tt, "new-fleet-"+theme)
			click(t, tt, "Create fleet")
			tt.Frame()
			mustText(t, tt, "That folder is not empty. Pick a new or empty folder.")
			shot(t, tt, "new-fleet-refused-"+theme)
			click(t, tt, "Create fleet")
			tt.Frame()
			mustText(t, tt, "Copy the command")
			shot(t, tt, "new-fleet-failed-"+theme)
		})
	}
}
