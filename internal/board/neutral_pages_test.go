package board_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
)

const nameMarker = "zq7xmarker"

type unauthenticated struct {
	what, method, path string
	form               url.Values
	cookies            []*http.Cookie
	headers            map[string]string
	status             int
}

func wholeResponse(rec *httptest.ResponseRecorder) string {
	var all strings.Builder
	all.WriteString(rec.Body.String())
	for k, vs := range rec.Result().Header {
		all.WriteString("\n" + k + ": " + strings.Join(vs, "\n"))
	}
	return all.String()
}

func neutralRequests(prefix string, f railFleet, forged string, cookies map[string]*http.Cookie) []unauthenticated {
	op := serving("operator@example.com")
	funnel := map[string]string{"Tailscale-Funnel-Request": "?1", "X-Forwarded-For": "203.0.113.9"}
	return []unauthenticated{
		{"pair form", "GET", prefix + "/pair", nil, nil, nil, 200},
		{"pair form through serve", "GET", prefix + "/pair", nil, nil, op, 200},
		{"pair with a wrong code", "POST", prefix + "/pair", url.Values{"code": {"AAAAA-AAAAA"}, "name": {"Pixel"}}, nil, op, 403},
		{"pair with no code", "POST", prefix + "/pair", url.Values{"name": {"Pixel"}}, nil, nil, 403},
		{"no cookie", "GET", prefix + "/", nil, nil, nil, 403},
		{"no cookie through serve", "GET", prefix + "/", nil, nil, op, 403},
		{"a page with no cookie", "GET", prefix + "/tasks", nil, nil, op, 403},
		{"the connect page with no cookie", "GET", prefix + "/connect", nil, nil, op, 403},
		{"the event stream with no cookie", "GET", prefix + "/events", nil, nil, op, 403},
		{"an unknown page with no cookie", "GET", prefix + "/nope", nil, nil, op, 403},
		{"a code with no cookie", "POST", prefix + "/connect/code", url.Values{"csrf": {f.token}, "base": {"https://x.example"}}, nil, op, 403},
		{"a wrong token link", "GET", prefix + "/?token=wrong", nil, nil, nil, 403},
		{"a revoked phone", "GET", prefix + "/", nil, []*http.Cookie{cookies["revoked"]}, op, 403},
		{"a lapsed phone", "GET", prefix + "/", nil, []*http.Cookie{cookies["lapsed"]}, op, 403},
		{"a phone for another login", "GET", prefix + "/", nil, []*http.Cookie{cookies["live"]}, serving("other@example.com"), 403},
		{"a phone reaching the board another way", "GET", prefix + "/", nil, []*http.Cookie{cookies["live"]}, nil, 403},
		{"a forged phone cookie", "GET", prefix + "/", nil, []*http.Cookie{{Name: "__Host-hand_phone" + strings.ReplaceAll(prefix, "/", "_"), Value: forged}}, op, 403},
		{"funnel", "GET", prefix + "/", nil, []*http.Cookie{cookies["live"]}, funnel, 403},
		{"funnel on the pair page", "GET", prefix + "/pair", nil, nil, funnel, 403},
	}
}

func TestNoPageWithoutASessionNamesTheFleet(t *testing.T) {
	p := newPhoneHost(t, true, nameMarker, "bravo")
	f := p.fleets[0]
	op := serving("operator@example.com")
	old := p.pair(f, op)
	p.advance(91 * 24 * time.Hour)
	live, gone := p.pair(f, op), p.pair(f, op)
	p.revoke(f, gone)
	cookies := map[string]*http.Cookie{"lapsed": old, "live": live, "revoked": gone}
	forged := "1." + strings.Repeat("a", 64)

	signedIn := p.send("GET", "/"+f.id+"/", nil, []*http.Cookie{live}, op)
	contains(t, "a signed-in page", signedIn.Body.String(), "<title>board · "+nameMarker+"</title>", `data-fleet="`+nameMarker+`"`)

	direct := board.New(f.st, f.token, board.Options{Now: p.clock})
	for _, where := range []struct {
		name   string
		prefix string
		serve  func(*http.Request) *httptest.ResponseRecorder
	}{
		{"host", "/" + f.id, func(r *http.Request) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			p.h.ServeHTTP(rec, r)
			return rec
		}},
		{"board", "", func(r *http.Request) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			direct.ServeHTTP(rec, r)
			return rec
		}},
	} {
		for _, c := range neutralRequests(where.prefix, f, forged, cookies) {
			var req *http.Request
			if c.form != nil {
				req = httptest.NewRequest(c.method, c.path, strings.NewReader(c.form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			} else {
				req = httptest.NewRequest(c.method, c.path, nil)
			}
			for _, k := range c.cookies {
				if where.prefix == "" && k.Name != "" {
					k = &http.Cookie{Name: "__Host-hand_phone", Value: k.Value}
				}
				req.AddCookie(k)
			}
			for k, v := range c.headers {
				req.Header.Set(k, v)
			}
			rec := where.serve(req)
			what := where.name + ": " + c.what
			if rec.Code != c.status {
				t.Fatalf("%s = %d, want %d:\n%s", what, rec.Code, c.status, rec.Body.String())
			}
			if strings.Contains(wholeResponse(rec), nameMarker) {
				t.Fatalf("%s names the fleet:\n%s", what, wholeResponse(rec))
			}
			body := rec.Body.String()
			lacks(t, what, body, `class="ref fleet-id"`, "bravo")
			if strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
				contains(t, what, body, `class="tint-0"`)
				if !strings.Contains(body, ` · this Board</title>`) && !strings.Contains(body, ` · hand</title>`) {
					t.Fatalf("%s has a title that is neither neutral nor the Host's:\n%s", what, body)
				}
			}
		}
	}
}
