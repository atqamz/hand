package board_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

const (
	strictCSP = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; media-src 'self'; frame-src 'self'; form-action 'self'; frame-ancestors 'none'; manifest-src 'self'; worker-src 'self'"
	priorCSP  = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'"
)

var regionOpen = regexp.MustCompile(`<section[^>]*data-region="(status|console|timeline|queue|tasks)"`)

func TestTheFleetPageRendersEveryRegionInline(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	page := get(t, fx.handler(), "/")
	lacks(t, "fleet page", page, "<iframe")
	for _, region := range []string{"status", "console", "timeline", "queue", "tasks"} {
		if n := strings.Count(page, `data-region="`+region+`"`); n != 1 {
			t.Fatalf("region %s appears %d times:\n%s", region, n, page)
		}
	}
	contains(t, "fleet page", page, `id="composer"`, `id="toast"`, `class="drop-veil"`)
	for _, el := range []string{`id="composer"`, `class="drop-veil"`} {
		at := strings.Index(page, el)
		for _, m := range regionOpen.FindAllStringIndex(page, -1) {
			end := strings.Index(page[m[0]:], "</section>")
			if at > m[0] && at < m[0]+end {
				t.Fatalf("%s sits inside a region:\n%s", el, page)
			}
		}
	}
}

func TestNoInlineScriptOrStyle(t *testing.T) {
	st := open(t)
	seed(t, st)
	fx := newFixture(t)
	fx.st = st
	h := fx.handler()
	pages := map[string]string{
		"fleet":    get(t, h, "/"),
		"task":     get(t, h, "/task/t1"),
		"decision": get(t, h, "/decision/d1"),
		"history":  get(t, h, "/supervisor/log"),
		"error":    request(h, "GET", "/task/t99", nil, true).Body.String(),
	}
	script := regexp.MustCompile(`<script(\s[^>]*)?>`)
	for name, body := range pages {
		lacks(t, name, body, "<style", ` style="`, "http-equiv")
		for _, tag := range script.FindAllString(body, -1) {
			if !strings.Contains(tag, "src=") {
				t.Fatalf("%s has an inline script %q", name, tag)
			}
		}
	}
}

func TestStaticAssetsAreHashedAndCached(t *testing.T) {
	h := newFixture(t).handler()
	page := get(t, h, "/")
	for _, c := range []struct{ name, ext, ctype string }{{"board", "css", "text/css"}, {"app", "js", "text/javascript"}} {
		m := regexp.MustCompile(`/static/` + c.name + `\.[0-9a-f]{12}\.` + c.ext).FindString(page)
		if m == "" {
			t.Fatalf("page does not link a hashed %s.%s:\n%s", c.name, c.ext, page)
		}
		rec := request(h, "GET", m, nil, false)
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), c.ctype) || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
			t.Fatalf("%s = %d %q %q", m, rec.Code, rec.Header().Get("Content-Type"), rec.Header().Get("Cache-Control"))
		}
	}
	if rec := request(h, "GET", "/static/board.000000000000.css", nil, false); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown asset = %d", rec.Code)
	}
}

func TestHeadersAreStrict(t *testing.T) {
	h := newFixture(t).handler()
	for _, p := range []string{"/", "/supervisor/log", "/task/t9"} {
		rec := request(h, "GET", p, nil, true)
		if got := rec.Header().Get("Content-Security-Policy"); got != strictCSP {
			t.Fatalf("%s CSP = %q", p, got)
		}
		if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Fatalf("%s X-Frame-Options = %q", p, got)
		}
	}
}

func directives(csp string) map[string]string {
	out := map[string]string{}
	for _, d := range strings.Split(csp, "; ") {
		name, value, _ := strings.Cut(d, " ")
		out[name] = value
	}
	return out
}

func TestThePageCSPGainedOnlyMediaFrameManifestAndWorkerSources(t *testing.T) {
	got := directives(strictCSP)
	want := directives(priorCSP)
	for _, name := range []string{"media-src", "frame-src", "manifest-src", "worker-src"} {
		want[name] = "'self'"
	}
	if len(got) != len(want) {
		t.Fatalf("directives = %v, want %v", got, want)
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %q, want %q", name, got[name], value)
		}
	}
	rec := request(newFixture(t).handler(), "GET", "/", nil, true)
	served := directives(rec.Header().Get("Content-Security-Policy"))
	for name, value := range want {
		if served[name] != value {
			t.Errorf("served %s = %q, want %q", name, served[name], value)
		}
	}
	if len(served) != len(want) {
		t.Errorf("served %v, want %v", served, want)
	}
}

func TestLinksFollowTheBase(t *testing.T) {
	st := open(t)
	seed(t, st)
	fleet, err := st.Fleet(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	base := "/" + fleet.ID
	fx := newFixture(t)
	fx.st = st
	fx.options.Base = base
	h := fx.handler()
	link := regexp.MustCompile(`(?:href|action)="([^"]*)"`)
	call := func(method, p string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, p, strings.NewReader("csrf="+token))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "hand_board_" + fleet.ID, Value: token})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for _, p := range []string{"/", "/task/t1", "/decision/d1"} {
		rec := call("GET", p)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", p, rec.Code)
		}
		body := rec.Body.String()
		for _, m := range link.FindAllStringSubmatch(body, -1) {
			u := m[1]
			if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "/static/") || strings.HasPrefix(u, "#") || u == "/" {
				continue
			}
			if !strings.HasPrefix(u, base+"/") {
				t.Fatalf("%s links %q outside the base", p, u)
			}
		}
	}
	rec := request(h, "GET", "/?token="+token, nil, false)
	if c := rec.Header().Get("Set-Cookie"); !strings.HasPrefix(c, "hand_board_"+fleet.ID+"="+token) || !strings.Contains(c, "Path=/;") {
		t.Fatalf("cookie = %q", c)
	}
	if loc := rec.Header().Get("Location"); loc != base+"/" {
		t.Fatalf("token redirect = %q", loc)
	}
	if loc := call("POST", "/supervisor/stop").Header().Get("Location"); loc != base+"/" {
		t.Fatalf("control redirect = %q", loc)
	}
	_ = board.New
}

func TestWaitingCountsAreNotCapped(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	if _, err := st.AddProject(ctx, "hand", handRepo, ""); err != nil {
		t.Fatal(err)
	}
	task, err := st.AddTask(ctx, "hand", "Many questions", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 501 {
		if _, err := st.Ask(ctx, task.ID, fmt.Sprintf("question %d?", i)); err != nil {
			t.Fatal(err)
		}
	}
	body := get(t, board.New(st, token, board.Options{}), "/")
	contains(t, "fleet page", body, `data-waiting="501"`, "451 more waiting")
}

func TestProofAnswersWithoutLogin(t *testing.T) {
	rec := request(board.New(open(t), token, board.Options{}), "GET", "/proof?nonce=abc123", nil, false)
	if rec.Code != http.StatusOK || rec.Body.String() != board.Proof(token, "abc123") || strings.Contains(rec.Body.String(), token) || board.Proof(token, "abc123") == board.Proof(token, "abc124") {
		t.Fatalf("proof = %d %q", rec.Code, rec.Body.String())
	}
}

func TestTheTaskPageIsOneRegion(t *testing.T) {
	st := open(t)
	seed(t, st)
	body := get(t, board.New(st, token, board.Options{}), "/task/t1")
	if strings.Count(body, "data-region=") != 1 || !strings.Contains(body, `<main class="thread" data-region="task">`) || strings.Contains(body, "http-equiv") {
		t.Fatalf("task page regions:\n%s", body)
	}
}
