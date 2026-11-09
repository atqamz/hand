package desktop_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
		if rest == "/proof" {
			if _, err := r.Cookie("hand_board_" + fleetID); err == nil {
				t.Error("the proof request carried the token")
			}
			mac := hmac.New(sha256.New, []byte(testToken))
			mac.Write([]byte(r.URL.Query().Get("nonce")))
			io.WriteString(w, hex.EncodeToString(mac.Sum(nil)))
			return
		}
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
	for i, p := range *paths {
		if strings.Contains(p, testToken) {
			t.Fatalf("the token is in the URL: %s", p)
		}
		if strings.Contains(p, "/api/v1/") && (i == 0 || !strings.Contains((*paths)[i-1], "/proof?")) {
			t.Fatalf("an API read went before its proof: %v", *paths)
		}
	}
}

func TestHTTPClientReportsTheAPIsErrors(t *testing.T) {
	srv, _ := goldenServer(t)
	c := goldenClient(t, srv.URL)
	ctx := context.Background()

	_, err := c.Task(ctx, fleetID, "t999")
	var api *desktop.APIError
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
				if strings.HasSuffix(r.URL.Path, "/proof") {
					proof(w, r)
					return
				}
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
	if err == nil || !strings.Contains(err.Error(), "answers at") {
		t.Fatalf("error = %v", err)
	}
}

func proof(w http.ResponseWriter, r *http.Request) {
	mac := hmac.New(sha256.New, []byte(testToken))
	mac.Write([]byte(r.URL.Query().Get("nonce")))
	io.WriteString(w, hex.EncodeToString(mac.Sum(nil)))
}

func TestTheClientWithholdsTheTokenFromAnotherService(t *testing.T) {
	var cookies int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("hand_board_" + fleetID); err == nil {
			cookies++
		}
		io.WriteString(w, "not a proof")
	}))
	defer srv.Close()
	c := goldenClient(t, srv.URL)
	_, err := c.Status(context.Background(), fleetID)
	if err == nil || !strings.Contains(err.Error(), "no board for fleet") {
		t.Fatalf("error = %v", err)
	}
	if cookies != 0 {
		t.Fatalf("the token went to a service that could not prove it is the board")
	}
}

func TestTheProofRequestDoesNotUseTheCookieJar(t *testing.T) {
	srv, _ := goldenServer(t)
	c := goldenClient(t, srv.URL)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(u, []*http.Cookie{{Name: "hand_board_" + fleetID, Value: testToken}})
	c.HTTP = &http.Client{Jar: jar}
	if _, err := c.Status(context.Background(), fleetID); err != nil {
		t.Fatal(err)
	}
}

func TestTheClientRefusesAProofRedirect(t *testing.T) {
	var cookies int
	board := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("hand_board_" + fleetID); err == nil {
			cookies++
		}
		if strings.HasSuffix(r.URL.Path, "/proof") {
			proof(w, r)
		}
	}))
	defer board.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("hand_board_" + fleetID); err == nil {
			cookies++
		}
		http.Redirect(w, r, board.URL+r.URL.Path+"?"+r.URL.RawQuery, http.StatusFound)
	}))
	defer srv.Close()
	c := goldenClient(t, srv.URL)
	_, err := c.Status(context.Background(), fleetID)
	if err == nil || !strings.Contains(err.Error(), "no board for fleet") {
		t.Fatalf("error = %v", err)
	}
	if cookies != 0 {
		t.Fatalf("the token went to a redirect target or the service at the recorded port")
	}
}

func TestTheClientRefusesAnAPIRedirect(t *testing.T) {
	var cookies int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("hand_board_" + fleetID); err == nil {
			cookies++
		}
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/proof") {
			proof(w, r)
			return
		}
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer srv.Close()
	c := goldenClient(t, srv.URL)
	if _, err := c.Status(context.Background(), fleetID); err == nil {
		t.Fatal("the client followed the API redirect")
	}
	if cookies != 0 {
		t.Fatalf("the token followed an API redirect")
	}
}

func TestHTTPClientReportsARejectedSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/proof") {
			proof(w, r)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"status":401,"code":"unauthorized","message":"this request needs the board's session cookie"}}`)
	}))
	defer srv.Close()
	c := goldenClient(t, srv.URL)
	_, err := c.Status(context.Background(), fleetID)
	var api *desktop.APIError
	if !errors.As(err, &api) || api.Code != "unauthorized" {
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
	mustText(t, tt, "fleet "+fleetID+": no board.token", "hand open")
	mustNotText(t, tt, "no fleets are registered")
}

func TestATokenCreatedAfterLaunchIsRead(t *testing.T) {
	root := t.TempDir()
	home, _ := writeLocal(t, root)
	token := filepath.Join(home, "board.token")
	if err := os.Remove(token); err != nil {
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
	if _, err := c.Token(fleetID); err == nil || !strings.Contains(err.Error(), "no board.token yet") {
		t.Fatalf("token error = %v", err)
	}
	if err := os.WriteFile(token, []byte(testToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := c.Token(fleetID)
	if err != nil || got != testToken {
		t.Fatalf("token = %q, %v after hand open wrote it", got, err)
	}
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

func TestAnUnreadableTokenStaysListedWithTheFix(t *testing.T) {
	root := t.TempDir()
	home, _ := writeLocal(t, root)
	token := filepath.Join(home, "board.token")
	if err := os.Remove(token); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(token, 0o700); err != nil {
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
	if fleets := c.Fleets(); len(fleets) != 1 || fleets[0].ID != fleetID {
		t.Fatalf("fleets = %+v", fleets)
	}
	if _, err := c.Token(fleetID); err == nil ||
		!strings.Contains(err.Error(), "board.token cannot be read") ||
		!strings.Contains(err.Error(), "hand open") {
		t.Fatalf("token error = %v", err)
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
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("the root is unreadable: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("the desktop wrote %v into its root", entries)
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

type mutations struct {
	mu       sync.Mutex
	badCSRF  bool
	accepted bool
	// controlAccepted answers a supervisor control with the accepted envelope
	// the board sends when it ran the action but could not read the status.
	controlAccepted bool
	body            string
	csrf            []string
	paths           []string
}

func mutationServer(t *testing.T) (*httptest.Server, *mutations) {
	t.Helper()
	m := &mutations{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := "/" + fleetID
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		if rest == "/proof" {
			proof(w, r)
			return
		}
		c, err := r.Cookie("hand_board_" + fleetID)
		if err != nil || c.Value != testToken {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":{"status":401,"code":"unauthorized","message":"this request needs the board's session cookie"}}`)
			return
		}
		if rest == "/api/v1/session" {
			csrf := testToken
			m.mu.Lock()
			if m.badCSRF {
				csrf = "wrong"
			}
			m.mu.Unlock()
			io.WriteString(w, `{"version":1,"fleet":{"id":"`+fleetID+`","name":"golden"},"cursor":"c","generated_at":"2026-11-01T05:00:00Z","data":{"csrf":"`+csrf+`"}}`)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		m.mu.Lock()
		m.csrf = append(m.csrf, r.Header.Get("X-Hand-CSRF"))
		m.paths = append(m.paths, r.Method+" "+rest)
		m.mu.Unlock()
		if r.Header.Get("X-Hand-CSRF") != testToken {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"error":{"status":403,"code":"csrf_required","message":"the X-Hand-CSRF header is missing or wrong"}}`)
			return
		}
		var name string
		switch rest {
		case "/api/v1/decision/d1/answer":
			name = "decision-answered"
		case "/api/v1/report/r1/ack":
			name = "report-acked"
		case "/api/v1/message":
			name = "message-sent"
		case "/api/v1/supervisor/start", "/api/v1/supervisor/resume", "/api/v1/supervisor/stop",
			"/api/v1/supervisor/interrupt", "/api/v1/supervisor/force", "/api/v1/supervisor/keys":
			name = "status"
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error":{"status":404,"code":"not_found","message":"not found"}}`)
			return
		}
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		m.mu.Lock()
		m.body = string(b)
		accepted, control := m.accepted, m.controlAccepted
		m.mu.Unlock()
		if (name == "message-sent" && accepted) || (name == "status" && control) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			io.WriteString(w, `{"version":1,"fleet":{"id":"`+fleetID+`","name":"golden"},"cursor":"c","generated_at":"2026-11-01T05:00:00Z","data":{"accepted":true}}`)
			return
		}
		data, err := os.ReadFile(filepath.Join("..", "board", "testdata", "api", name+".json"))
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv, m
}

func TestHTTPClientAnswersAndAcks(t *testing.T) {
	srv, m := mutationServer(t)
	c := goldenClient(t, srv.URL)
	ctx := context.Background()

	dd, err := c.AnswerDecision(ctx, fleetID, "d1", "1. Yes, keep it", "keep the docs")
	if err != nil {
		t.Fatal(err)
	}
	if dd.Decision.Status != "answered" || !strings.Contains(dd.Decision.Answer, "keep the docs") {
		t.Fatalf("decision = %+v", dd.Decision)
	}
	m.mu.Lock()
	body, csrf := m.body, append([]string(nil), m.csrf...)
	m.mu.Unlock()
	if !strings.Contains(body, `"choice":"1. Yes, keep it"`) || !strings.Contains(body, `"note":"keep the docs"`) {
		t.Fatalf("request body = %q", body)
	}
	if len(csrf) != 1 || csrf[0] != testToken {
		t.Fatalf("CSRF headers = %v", csrf)
	}

	rd, err := c.AckReport(ctx, fleetID, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if rd.Report.AckedBy != "operator (desktop)" || rd.Report.AckedAt == "" {
		t.Fatalf("report = %+v", rd.Report)
	}
}

func TestHTTPClientSendsAMessage(t *testing.T) {
	srv, m := mutationServer(t)
	c := goldenClient(t, srv.URL)
	sent, err := c.SendMessage(context.Background(), fleetID, "hello from the desktop")
	if err != nil {
		t.Fatal(err)
	}
	tl := sent.Timeline
	if !sent.Fresh || len(tl.Entries) == 0 || tl.Entries[0].Role != "operator" || tl.Entries[0].Text != "hello from the desktop" || !tl.Entries[0].Queued {
		t.Fatalf("sent = %+v", sent)
	}
	m.mu.Lock()
	body, csrf := m.body, append([]string(nil), m.csrf...)
	m.mu.Unlock()
	if !strings.Contains(body, `"text":"hello from the desktop"`) {
		t.Fatalf("request body = %q", body)
	}
	if len(csrf) != 1 || csrf[0] != testToken {
		t.Fatalf("CSRF headers = %v", csrf)
	}
}

func TestHTTPClientReportsAnAcceptedMessageWithoutATimeline(t *testing.T) {
	srv, m := mutationServer(t)
	m.mu.Lock()
	m.accepted = true
	m.mu.Unlock()
	c := goldenClient(t, srv.URL)
	sent, err := c.SendMessage(context.Background(), fleetID, "hello from the desktop")
	if err != nil {
		t.Fatal(err)
	}
	if sent.Fresh || len(sent.Timeline.Entries) != 0 {
		t.Fatalf("sent = %+v", sent)
	}
	m.mu.Lock()
	body := m.body
	m.mu.Unlock()
	if !strings.Contains(body, `"text":"hello from the desktop"`) {
		t.Fatalf("request body = %q", body)
	}
}

func TestHTTPClientPostsSupervisorControls(t *testing.T) {
	srv, m := mutationServer(t)
	c := goldenClient(t, srv.URL)
	ctx := context.Background()
	ctl, err := c.Supervise(ctx, fleetID, "stop", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if ctl.Stale || ctl.Status.Supervisor == nil || ctl.Status.Supervisor.Ref != "s1" {
		t.Fatalf("control = %+v", ctl)
	}
	digest := "0123456789abcdef0123456789abcdef"
	ctl, err = c.PressKeys(ctx, fleetID, []string{"down", "enter"}, 6, digest, 2, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if ctl.Stale || ctl.Status.Supervisor == nil {
		t.Fatalf("control = %+v", ctl)
	}
	m.mu.Lock()
	body, csrf, paths := m.body, append([]string(nil), m.csrf...), append([]string(nil), m.paths...)
	m.mu.Unlock()
	if len(paths) != 2 || paths[0] != "POST /api/v1/supervisor/stop" || paths[1] != "POST /api/v1/supervisor/keys" {
		t.Fatalf("requests = %v", paths)
	}
	if len(csrf) != 2 || csrf[0] != testToken || csrf[1] != testToken {
		t.Fatalf("CSRF headers = %v", csrf)
	}
	if !strings.Contains(body, `"keys":["down","enter"]`) || !strings.Contains(body, `"revision":6`) ||
		!strings.Contains(body, `"digest":"`+digest+`"`) || !strings.Contains(body, `"after":2`) || !strings.Contains(body, `"ref":"s1"`) {
		t.Fatalf("keys body = %q", body)
	}
	if _, err := c.Supervise(ctx, fleetID, "nope", ""); err == nil {
		t.Fatal("an unknown control was sent")
	}
	if _, err := c.Supervise(ctx, fleetID, "stop", ""); err == nil {
		t.Fatal("stop without the displayed supervisor was sent")
	}
}

func TestHTTPClientReportsAControlThatOutranItsStatus(t *testing.T) {
	srv, m := mutationServer(t)
	m.mu.Lock()
	m.controlAccepted = true
	m.mu.Unlock()
	c := goldenClient(t, srv.URL)
	ctl, err := c.Supervise(context.Background(), fleetID, "interrupt", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !ctl.Stale || ctl.Status.Supervisor != nil {
		t.Fatalf("control = %+v", ctl)
	}
	m.mu.Lock()
	body := m.body
	m.mu.Unlock()
	if !strings.Contains(body, `"ref":"s1"`) {
		t.Fatalf("interrupt body = %q", body)
	}
}

func TestHTTPClientReportsACSRFRefusal(t *testing.T) {
	srv, m := mutationServer(t)
	m.badCSRF = true
	c := goldenClient(t, srv.URL)
	_, err := c.AnswerDecision(context.Background(), fleetID, "d1", "1. Yes, keep it", "")
	var api *desktop.APIError
	if !errors.As(err, &api) || api.Status != http.StatusForbidden || api.Code != "csrf_required" {
		t.Fatalf("error = %v", err)
	}
}
