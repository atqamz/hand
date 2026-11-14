package desktop_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/desktop"
)

type startSpy struct {
	mu    sync.Mutex
	calls int
	home  string
	block chan struct{}
	line  string
	err   error
}

func (s *startSpy) run(ctx context.Context, home string) (string, error) {
	s.mu.Lock()
	s.calls++
	s.home = home
	block, line, err := s.block, s.line, s.err
	s.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return line, err
}

func (s *startSpy) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *startSpy) waitStarts(t *testing.T, want int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if s.count() >= want {
			return
		}
	}
	t.Fatalf("starts = %d, want %d", s.count(), want)
}

func startApp(t *testing.T, connect func() (desktop.ReadModel, error), start func(context.Context, string) (string, error)) *desktop.App {
	t.Helper()
	app := desktop.New(connect)
	app.DraftDir = t.TempDir()
	app.Start = start
	app.Boot()
	t.Cleanup(app.Close)
	return app
}

type offlineModel struct{ *fixture }

func (o offlineModel) Name(context.Context, string) (string, error) {
	return "", errors.New("dial tcp 127.0.0.1:7777: connect: connection refused")
}

func TestTheWindowStartsTheBoardWhenNoneIsRunning(t *testing.T) {
	f := goldenFixture(t)
	first := true
	connect := func() (desktop.ReadModel, error) {
		if first {
			first = false
			return nil, desktop.ErrNoBoard
		}
		return f, nil
	}
	spy := &startSpy{}
	app := startApp(t, connect, spy.run)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	app.StartRefresh()
	spy.waitStarts(t, 1)
	tt.Frame()
	mustText(t, tt, "Starting the Board…")
	deadline := time.After(5 * time.Second)
	for !tt.HasText("Fix login") {
		select {
		case fn := <-posted:
			fn()
		case <-deadline:
			t.Fatalf("the window never read the board: %q", tt.Texts())
		}
		tt.Frame()
	}
	mustText(t, tt, "golden")
	mustNotText(t, tt, "Starting the Board…")
}

func TestTheWindowStartsTheBoardWhenTheProofDoesNotAnswer(t *testing.T) {
	spy := &startSpy{}
	app := startApp(t, func() (desktop.ReadModel, error) { return offlineModel{goldenFixture(t)}, nil }, spy.run)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	app.StartRefresh()
	spy.waitStarts(t, 1)
	tt.Frame()
	mustText(t, tt, "Starting the Board…")
	waitPost(t, posted)()
	tt.Frame()
	if spy.count() != 1 {
		t.Fatalf("starts = %d after the first, want 1", spy.count())
	}
}

func TestTheWindowStartsNothingWhenSomethingAnswers(t *testing.T) {
	spy := &startSpy{}
	app := startApp(t, func() (desktop.ReadModel, error) { return goldenFixture(t), nil }, spy.run)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	app.StartRefresh()
	tt.Frame()
	mustText(t, tt, "golden", "Fix login")
	if spy.count() != 0 {
		t.Fatalf("starts = %d, want 0", spy.count())
	}
}

func TestTheWindowStartsNothingWhenTheBoardRefusesTheToken(t *testing.T) {
	spy := &startSpy{}
	app := startApp(t, func() (desktop.ReadModel, error) { return errorFixture(), nil }, spy.run)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	app.StartRefresh()
	tt.Frame()
	mustText(t, tt, "The Board refused this window.", "hand open")
	if spy.count() != 0 {
		t.Fatalf("starts = %d, want 0", spy.count())
	}
}

func TestStartingBoardDoesNotBlockTheWindow(t *testing.T) {
	release := make(chan struct{})
	spy := &startSpy{block: release}
	app := startApp(t, func() (desktop.ReadModel, error) { return nil, desktop.ErrNoBoard }, spy.run)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	app.StartRefresh()
	done := make(chan struct{})
	go func() {
		tt.Frame()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the window's loop waited for the start")
	}
	mustText(t, tt, "Starting the Board…")
	close(release)
	waitPost(t, posted)()
}

func TestClosingTheWindowDoesNotCancelTheStart(t *testing.T) {
	release := make(chan struct{})
	started, done := make(chan struct{}), make(chan error, 1)
	app := desktop.New(func() (desktop.ReadModel, error) { return nil, desktop.ErrNoBoard })
	app.DraftDir = t.TempDir()
	app.Start = func(ctx context.Context, _ string) (string, error) {
		close(started)
		<-release
		done <- ctx.Err()
		return "", nil
	}
	app.Boot()
	app.Post = func(func()) {}
	app.StartRefresh()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the start never ran")
	}
	app.Close()
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("closing the window cancelled the start: %v", err)
	}
}

func TestTheDidNotStartCardShowsTheLastLineAndTriesAgain(t *testing.T) {
	const last = "hand board: listen tcp 127.0.0.1:7777: bind: address already in use"
	spy := &startSpy{line: last, err: errors.New(last)}
	app := startApp(t, func() (desktop.ReadModel, error) { return nil, desktop.ErrNoBoard }, spy.run)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	app.StartRefresh()
	waitPost(t, posted)()
	tt.Frame()
	mustText(t, tt, "The Board did not start.", last, "Try again")
	if n := countText(t, tt, last); n != 1 {
		t.Fatalf("%q shows %d times", last, n)
	}
	click(t, tt, "Try again")
	waitPost(t, posted)()
	if spy.count() != 2 {
		t.Fatalf("starts = %d, want 2", spy.count())
	}
}

func TestTheNoFleetsCardSaysHowToMakeOne(t *testing.T) {
	spy := &startSpy{}
	app := startApp(t, func() (desktop.ReadModel, error) { return noFleetsFixture(), nil }, spy.run)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	app.StartRefresh()
	tt.Frame()
	mustText(t, tt, "No fleet on this computer yet.", "cd ~/work && hand init", "Look again")
	if spy.count() != 0 {
		t.Fatalf("starts = %d, want 0", spy.count())
	}
}

func TestStartBoardStartsOnlyWhenNothingAnswers(t *testing.T) {
	srv := newAnsweringServer(t)
	root := t.TempDir()
	getenv := func(k string) string {
		if k == "SECONDHAND_HOME" {
			return root
		}
		return ""
	}
	if err := writeAddr(root, srv); err != nil {
		t.Fatal(err)
	}
	if line, err := desktop.StartBoard(getenv)(context.Background(), ""); err != nil || line != "" {
		t.Fatalf("an answering board was started over: %q %v", line, err)
	}
	if err := removeAddr(root); err != nil {
		t.Fatal(err)
	}
	line, err := desktop.StartBoard(getenv)(context.Background(), "")
	if !errors.Is(err, desktop.ErrNoFleets) || line != "" {
		t.Fatalf("no fleets: %q %v", line, err)
	}
	if err := writeDeadAddr(root); err != nil {
		t.Fatal(err)
	}
	line, err = desktop.StartBoard(getenv)(context.Background(), "")
	if !errors.Is(err, desktop.ErrNoFleets) || line != "" {
		t.Fatalf("a dead address: %q %v", line, err)
	}
}

func TestStartBoardRunsTheVerbInTheFleetFolder(t *testing.T) {
	hand := installFakeHand(t)
	root := t.TempDir()
	fleet, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fleet, "hand.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "index"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index", fleetID), []byte(fleet+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	getenv := func(k string) string {
		if k == "SECONDHAND_HOME" {
			return root
		}
		return ""
	}
	start := desktop.StartBoard(getenv)
	if line, err := start(context.Background(), ""); err != nil || line != "" {
		t.Fatalf("start = %q %v", line, err)
	}
	hand.wantIn(t, fleet, "board", "start")

	t.Setenv("FAKE_HAND_FAIL", "the board cannot start")
	line, err := start(context.Background(), "")
	if err == nil || line != "the board cannot start" {
		t.Fatalf("start = %q %v", line, err)
	}

	t.Setenv("FAKE_HAND_FAIL", "")
	t.Setenv("FAKE_HAND_SLEEP", "1")
	restore := desktop.SetBoardStartTimeout(150 * time.Millisecond)
	defer restore()
	line, err = start(context.Background(), "")
	if err == nil || line != "`hand board start` did not finish in time" {
		t.Fatalf("start = %q %v", line, err)
	}
}

func TestStartBoardDoesNotStartWhenTheProofFails(t *testing.T) {
	hand := installFakeHand(t)
	root := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	if err := writeAddr(root, srv); err != nil {
		t.Fatal(err)
	}
	getenv := func(k string) string {
		if k == "SECONDHAND_HOME" {
			return root
		}
		return ""
	}
	if line, err := desktop.StartBoard(getenv)(context.Background(), ""); err != nil || line != "" {
		t.Fatalf("start = %q %v", line, err)
	}
	if got := hand.calls(t); len(got) != 0 {
		t.Fatalf("the verb ran: %q", got)
	}
}

func TestABoardThatStopsAfterBootStartsOnce(t *testing.T) {
	f := goldenFixture(t)
	var mu sync.Mutex
	connects := 0
	connect := func() (desktop.ReadModel, error) {
		mu.Lock()
		defer mu.Unlock()
		connects++
		if connects == 1 {
			return f, nil
		}
		return offlineModel{f}, nil
	}
	spy := &startSpy{}
	app := startApp(t, connect, spy.run)
	posted := make(chan func(), 8)
	app.Post = func(fn func()) { posted <- fn }
	tt := newTester(app)
	app.StartRefresh()
	tt.Frame()
	mustText(t, tt, "golden", "Fix login")
	app.Refresh()
	deadline := time.After(5 * time.Second)
	for spy.count() < 1 {
		select {
		case fn := <-posted:
			fn()
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatalf("the window never started the board: %q", tt.Texts())
		}
	}
	for settle := time.Now().Add(500 * time.Millisecond); time.Now().Before(settle); {
		select {
		case fn := <-posted:
			fn()
		case <-time.After(10 * time.Millisecond):
		}
	}
	if got := spy.count(); got != 1 {
		t.Fatalf("starts = %d, want 1", got)
	}
}

func TestTheCopyButtonSaysCopied(t *testing.T) {
	app := startApp(t, func() (desktop.ReadModel, error) { return noFleetsFixture(), nil }, (&startSpy{}).run)
	app.Post = func(func()) {}
	tt := newTester(app)
	tt.Frame()
	click(t, tt, "Copy the command")
	mustText(t, tt, "Copied")
	mustNotText(t, tt, "Copy the command")
	tt.Advance(2 * time.Second)
	tt.Frame()
	mustText(t, tt, "Copy the command")
}

func TestScenesStartingTheBoard(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		t.Run(theme, func(t *testing.T) {
			release := make(chan struct{})
			spy := &startSpy{block: release}
			app := startApp(t, func() (desktop.ReadModel, error) { return nil, desktop.ErrNoBoard }, spy.run)
			posted := make(chan func(), 8)
			app.Post = func(fn func()) { posted <- fn }
			app.StartRefresh()
			tt := darkTester(app, dark)
			mustText(t, tt, "Starting the Board…")
			shot(t, tt, "starting-board-"+theme)
			close(release)
			waitPost(t, posted)()
		})
	}
}

func TestScenesBoardDidNotStart(t *testing.T) {
	const last = "hand board: listen tcp 127.0.0.1:7777: bind: address already in use"
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		t.Run(theme, func(t *testing.T) {
			spy := &startSpy{line: last, err: errors.New(last)}
			app := startApp(t, func() (desktop.ReadModel, error) { return nil, desktop.ErrNoBoard }, spy.run)
			posted := make(chan func(), 8)
			app.Post = func(fn func()) { posted <- fn }
			app.StartRefresh()
			waitPost(t, posted)()
			tt := darkTester(app, dark)
			mustText(t, tt, "The Board did not start.", last, "Try again")
			shot(t, tt, "board-did-not-start-"+theme)
		})
	}
}

func TestScenesNoFleets(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		t.Run(theme, func(t *testing.T) {
			app := startApp(t, func() (desktop.ReadModel, error) { return noFleetsFixture(), nil }, (&startSpy{}).run)
			app.Post = func(func()) {}
			tt := darkTester(app, dark)
			mustText(t, tt, "No fleet on this computer yet.", "cd ~/work && hand init")
			shot(t, tt, "no-fleets-"+theme)
		})
	}
}

func TestScenesBoardRefused(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := map[bool]string{false: "light", true: "dark"}[dark]
		t.Run(theme, func(t *testing.T) {
			app := startApp(t, func() (desktop.ReadModel, error) { return errorFixture(), nil }, (&startSpy{}).run)
			app.Post = func(func()) {}
			tt := darkTester(app, dark)
			mustText(t, tt, "The Board refused this window.", "hand open", "Try again")
			shot(t, tt, "board-refused-"+theme)
		})
	}
}

func newAnsweringServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeAddr(root string, srv *httptest.Server) error {
	u, err := url.Parse(srv.URL)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "board.addr"), []byte(u.Host+"\n"), 0o600)
}

func removeAddr(root string) error { return os.Remove(filepath.Join(root, "board.addr")) }

func writeDeadAddr(root string) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "board.addr"), []byte(addr+"\n"), 0o600)
}
