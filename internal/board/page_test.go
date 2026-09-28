package board_test

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

const strictCSP = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'"

var regionOpen = regexp.MustCompile(`<section[^>]*data-region="(status|timeline|queue|tasks)"`)

func TestTheFleetPageRendersEveryRegionInline(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	page := get(t, fx.handler(), "/")
	lacks(t, "fleet page", page, "<iframe")
	for _, region := range []string{"status", "timeline", "queue", "tasks"} {
		if n := strings.Count(page, `data-region="`+region+`"`); n != 1 {
			t.Fatalf("region %s appears %d times:\n%s", region, n, page)
		}
	}
	contains(t, "fleet page", page, `id="composer"`, `id="toast"`)
	composer := strings.Index(page, `id="composer"`)
	for _, m := range regionOpen.FindAllStringIndex(page, -1) {
		end := strings.Index(page[m[0]:], "</section>")
		if composer > m[0] && composer < m[0]+end {
			t.Fatalf("the composer sits inside a region:\n%s", page)
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

func TestLinksFollowTheBase(t *testing.T) {
	st := open(t)
	seed(t, st)
	fx := newFixture(t)
	fx.st = st
	fx.options.Base = "/f000000000001"
	h := fx.handler()
	link := regexp.MustCompile(`(?:href|action)="([^"]*)"`)
	for _, p := range []string{"/", "/task/t1", "/decision/d1"} {
		body := get(t, h, p)
		for _, m := range link.FindAllStringSubmatch(body, -1) {
			u := m[1]
			if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "/static/") || strings.HasPrefix(u, "#") {
				continue
			}
			if !strings.HasPrefix(u, "/f000000000001/") {
				t.Fatalf("%s links %q outside the base", p, u)
			}
		}
	}
	rec := request(h, "GET", "/?token="+token, nil, false)
	if c := rec.Header().Get("Set-Cookie"); !strings.Contains(c, "Path=/f000000000001/") {
		t.Fatalf("cookie = %q", c)
	}
	if loc := rec.Header().Get("Location"); loc != "/f000000000001/" {
		t.Fatalf("token redirect = %q", loc)
	}
	res := post(h, "/supervisor/stop", nil)
	if loc := res.Header.Get("Location"); loc != "/f000000000001/" {
		t.Fatalf("control redirect = %q", loc)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = board.New
}
