package desktop_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef"

func goldenServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	files := map[string]string{
		"/api/v1/status":      "status",
		"/api/v1/timeline":    "timeline",
		"/api/v1/tasks":       "tasks",
		"/api/v1/task/t1":     "task",
		"/api/v1/attempt/a1":  "attempt-running",
		"/api/v1/decision/d1": "decision",
		"/api/v1/report/r1":   "report",
	}
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		prefix := "/" + fleetID
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		name, ok := files[rest]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error":{"status":404,"code":"not_found","message":"not found"}}`)
			return
		}
		c, err := r.Cookie("hand_board_" + fleetID)
		if err != nil || c.Value != testToken {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":{"status":401,"code":"unauthorized","message":"this request needs the board's session cookie"}}`)
			return
		}
		b, err := os.ReadFile(filepath.Join("..", "board", "testdata", "api", name+".json"))
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

func goldenClient(t *testing.T, base string) *desktop.HTTPClient {
	t.Helper()
	return &desktop.HTTPClient{
		Base:  base,
		Known: []desktop.Fleet{{ID: fleetID, Name: "golden"}},
		Token: func(fleet string) (string, error) {
			if fleet != fleetID {
				return "", errors.New("no token")
			}
			return testToken, nil
		},
		HTTP: http.DefaultClient,
	}
}

func TestHTTPClientReadsTheGoldenAPI(t *testing.T) {
	srv, paths := goldenServer(t)
	c := goldenClient(t, srv.URL)
	ctx := context.Background()

	if fleets := c.Fleets(); len(fleets) != 1 || fleets[0].ID != fleetID {
		t.Fatalf("fleets = %+v", fleets)
	}
	if name, err := c.Name(ctx, fleetID); err != nil || name != "golden" {
		t.Fatalf("name = %q, %v", name, err)
	}
	st, err := c.Status(ctx, fleetID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Pill != "working" || st.Needs.Waiting != 4 || len(st.Needs.Items) != 4 || st.Needs.Items[0].Word != "FAILED" {
		t.Fatalf("status = %+v", st)
	}
	if st.Supervisor == nil || st.Supervisor.Ref != "s1" || !st.Supervisor.Live {
		t.Fatalf("supervisor = %+v", st.Supervisor)
	}
	tl, err := c.Timeline(ctx, fleetID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Entries) != 3 || tl.Working == nil || tl.Working.Ref != "s1" || tl.Entries[0].Text != "All good." {
		t.Fatalf("timeline = %+v", tl)
	}
	ts, err := c.Tasks(ctx, fleetID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts.Groups) != 2 || ts.Groups[0].Name != "Active" || ts.Groups[0].Tasks[0].Ref != "t1" {
		t.Fatalf("tasks = %+v", ts)
	}
	td, err := c.Task(ctx, fleetID, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if td.Plan == nil || !strings.Contains(td.Plan.Body, "fix the cookie") || len(td.Attempts) != 1 || td.Attempts[0].State != "running" {
		t.Fatalf("task = %+v", td)
	}
	ad, err := c.Attempt(ctx, fleetID, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if ad.Brief != "x" || ad.Duration != "20m" || len(ad.Reports) != 1 {
		t.Fatalf("attempt = %+v", ad)
	}
	dd, err := c.Decision(ctx, fleetID, "d1")
	if err != nil {
		t.Fatal(err)
	}
	if len(dd.Options) != 2 || dd.Options[1].Label != "No, rename it" || !strings.Contains(dd.Body, "Yes, keep it") {
		t.Fatalf("decision = %+v", dd)
	}
	rd, err := c.Report(ctx, fleetID, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rd.Report.Body, "All tests pass.") || rd.Task.Ref != "t1" {
		t.Fatalf("report = %+v", rd)
	}
	for _, p := range *paths {
		if strings.Contains(p, testToken) {
			t.Fatalf("the token is in the URL: %s", p)
		}
	}
}

func TestHTTPClientReportsTheAPIsErrors(t *testing.T) {
	srv, _ := goldenServer(t)
	c := goldenClient(t, srv.URL)
	ctx := context.Background()

	c.Token = func(string) (string, error) { return "wrong", nil }
	_, err := c.Status(ctx, fleetID)
	var api *desktop.APIError
	if !errors.As(err, &api) || api.Code != "unauthorized" {
		t.Fatalf("error = %v", err)
	}
	c.Token = func(string) (string, error) { return testToken, nil }
	_, err = c.Task(ctx, fleetID, "t999")
	if !errors.As(err, &api) || api.Status != 404 || api.Code != "not_found" {
		t.Fatalf("error = %v", err)
	}
	if _, err := c.Status(ctx, "not-a-fleet"); err == nil {
		t.Fatal("a malformed fleet id was accepted")
	}
	c.Token = func(string) (string, error) { return "", errors.New("no board.token yet") }
	if _, err := c.Status(ctx, fleetID); err == nil || !strings.Contains(err.Error(), "board.token") {
		t.Fatalf("error = %v", err)
	}
}

func TestHTTPClientRejectsAnotherContract(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"version", `{"version":2,"fleet":{"id":"` + fleetID + `","name":"x"},"data":{}}`},
		{"fleet", `{"version":1,"fleet":{"id":"f00000000e0ab","name":"x"},"data":{}}`},
		{"not json", `not an envelope`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := goldenClient(t, srv.URL)
			if _, err := c.Status(context.Background(), fleetID); err == nil {
				t.Fatal("the client accepted another contract")
			}
		})
	}
}

func TestHTTPClientReportsATransportError(t *testing.T) {
	srv, _ := goldenServer(t)
	c := goldenClient(t, srv.URL)
	srv.Close()
	_, err := c.Status(context.Background(), fleetID)
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("error = %v", err)
	}
}

func writeLocal(t *testing.T, root string) (string, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "a-fleet")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "fleets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "hand.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "board.token"), []byte(testToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fleets", fleetID), []byte(home+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "board.addr"), []byte("127.0.0.1:7777\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, fleetID
}

func TestNewLocalFindsTheBoardAndTokens(t *testing.T) {
	root := t.TempDir()
	writeLocal(t, root)
	c, err := desktop.NewLocal(func(key string) string {
		if key == "SECONDHAND_HOME" {
			return root
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Base != "http://127.0.0.1:7777" {
		t.Fatalf("base = %q", c.Base)
	}
	fleets := c.Fleets()
	if len(fleets) != 1 || fleets[0].ID != fleetID || fleets[0].Name != fleetID {
		t.Fatalf("fleets = %+v", fleets)
	}
	token, err := c.Token(fleetID)
	if err != nil || token != testToken {
		t.Fatalf("token = %q, %v", token, err)
	}
}

func TestNewLocalListsAFleetWithoutAToken(t *testing.T) {
	root := t.TempDir()
	home, _ := writeLocal(t, root)
	if err := os.Remove(filepath.Join(home, "board.token")); err != nil {
		t.Fatal(err)
	}
	c, err := desktop.NewLocal(func(key string) string {
		if key == "SECONDHAND_HOME" {
			return root
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	fleets := c.Fleets()
	if len(fleets) != 1 || fleets[0].ID != fleetID {
		t.Fatalf("fleets = %+v", fleets)
	}
	if _, err := c.Token(fleetID); err == nil || !strings.Contains(err.Error(), "hand open") {
		t.Fatalf("token error = %v", err)
	}
}

func TestATokenlessFleetIsNamedInTheError(t *testing.T) {
	root := t.TempDir()
	home, _ := writeLocal(t, root)
	if err := os.Remove(filepath.Join(home, "board.token")); err != nil {
		t.Fatal(err)
	}
	c, err := desktop.NewLocal(func(key string) string {
		if key == "SECONDHAND_HOME" {
			return root
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	app := desktop.New(func() (desktop.ReadModel, error) { return c, nil })
	app.Boot()
	tt := newTester(app)
	mustText(t, tt, fleetID, "no board.token", "hand open")
	mustNotText(t, tt, "no fleets are registered")
}

func TestALocalTokenMustBeAToken(t *testing.T) {
	root := t.TempDir()
	home, _ := writeLocal(t, root)
	if err := os.WriteFile(filepath.Join(home, "board.token"), []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := desktop.NewLocal(func(key string) string {
		if key == "SECONDHAND_HOME" {
			return root
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Token(fleetID); err == nil || !strings.Contains(err.Error(), "not a board token") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewLocalWithoutABoard(t *testing.T) {
	root := t.TempDir()
	if _, err := desktop.NewLocal(func(key string) string {
		if key == "SECONDHAND_HOME" {
			return root
		}
		return ""
	}); !errors.Is(err, desktop.ErrNoBoard) {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("the desktop created its root: %v", err)
	}
}

func TestNewLocalRefusesANetworkBoard(t *testing.T) {
	root := t.TempDir()
	writeLocal(t, root)
	if err := os.WriteFile(filepath.Join(root, "board.addr"), []byte("192.168.1.5:7777\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := desktop.NewLocal(func(key string) string {
		if key == "SECONDHAND_HOME" {
			return root
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewLocalUsesHOME(t *testing.T) {
	home := t.TempDir()
	writeLocal(t, filepath.Join(home, ".secondhand"))
	c, err := desktop.NewLocal(func(key string) string {
		if key == "HOME" {
			return home
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Fleets()) != 1 {
		t.Fatalf("fleets = %+v", c.Fleets())
	}
}
