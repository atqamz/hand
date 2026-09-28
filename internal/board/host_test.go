package board_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

const (
	fleetA = "f00000000000a"
	fleetB = "f00000000000b"
)

type closer func() error

func (c closer) Close() error { return c() }

type fakeFleets struct {
	mu     sync.Mutex
	homes  map[string]string
	opened []string
	closed []string
}

func newFakeFleets() *fakeFleets {
	return &fakeFleets{homes: map[string]string{fleetA: "/homes/a", fleetB: "/homes/b"}}
}

func (f *fakeFleets) resolve(id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	home, ok := f.homes[id]
	if !ok {
		return "", fmt.Errorf("%w: fleet %s", state.ErrNotFound, id)
	}
	return home, nil
}

func (f *fakeFleets) open(id, home string) (http.Handler, io.Closer, error) {
	f.mu.Lock()
	f.opened = append(f.opened, id+" "+home)
	f.mu.Unlock()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "%s %s %s", id, home, r.URL.Path)
	})
	return h, closer(func() error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.closed = append(f.closed, id+" "+home)
		return nil
	}), nil
}

func (f *fakeFleets) seen() (opened, closed []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.opened), slices.Clone(f.closed)
}

func (f *fakeFleets) host(loopback bool) *board.Host {
	return board.NewHost(board.HostOptions{Loopback: loopback, Resolve: f.resolve, Open: f.open, List: func() ([]board.FleetLink, error) {
		return []board.FleetLink{{ID: fleetA, Name: "work"}, {ID: fleetB, Name: "play"}}, nil
	}})
}

func fetch(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func TestHostRoutesByFleetID(t *testing.T) {
	h := newFakeFleets().host(true)
	if rec := fetch(h, "/"+fleetA+"/x"); rec.Code != http.StatusOK || rec.Body.String() != fleetA+" /homes/a /x" {
		t.Fatalf("A = %d %q", rec.Code, rec.Body.String())
	}
	if rec := fetch(h, "/"+fleetB+"/"); rec.Code != http.StatusOK || rec.Body.String() != fleetB+" /homes/b /" {
		t.Fatalf("B = %d %q", rec.Code, rec.Body.String())
	}
	for _, p := range []string{"/nope/", "/F00000000000A/", "/f00000000000a0/"} {
		if rec := fetch(h, p); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "fleet not found") {
			t.Fatalf("%s = %d %q", p, rec.Code, rec.Body.String())
		}
	}
	rec := fetch(h, "/"+fleetA+"?token=x")
	if rec.Code != http.StatusTemporaryRedirect || rec.Header().Get("Location") != "/"+fleetA+"/?token=x" {
		t.Fatalf("no slash = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	css := regexp.MustCompile(`href="(/static/[^"]+)"`).FindStringSubmatch(fetch(h, "/").Body.String())
	if css == nil {
		t.Fatal("the fleet list links no stylesheet")
	}
	if rec := fetch(h, css[1]); rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("static = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestHostCachesUntilTheHomeChanges(t *testing.T) {
	f := newFakeFleets()
	h := f.host(true)
	fetch(h, "/"+fleetA+"/")
	fetch(h, "/"+fleetA+"/task/t1")
	if opened, closed := f.seen(); len(opened) != 1 || len(closed) != 0 {
		t.Fatalf("two requests: opened %q, closed %q", opened, closed)
	}
	f.mu.Lock()
	f.homes[fleetA] = "/homes/a2"
	f.mu.Unlock()
	if rec := fetch(h, "/"+fleetA+"/"); rec.Body.String() != fleetA+" /homes/a2 /" {
		t.Fatalf("after the move = %q", rec.Body.String())
	}
	opened, closed := f.seen()
	if !slices.Equal(opened, []string{fleetA + " /homes/a", fleetA + " /homes/a2"}) || !slices.Equal(closed, []string{fleetA + " /homes/a"}) {
		t.Fatalf("after the move: opened %q, closed %q", opened, closed)
	}
	fetch(h, "/"+fleetB+"/")
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if _, closed := f.seen(); len(closed) != 3 || !slices.Contains(closed, fleetA+" /homes/a2") || !slices.Contains(closed, fleetB+" /homes/b") {
		t.Fatalf("after Close: closed %q", closed)
	}
}

func TestADeadFleetIs404(t *testing.T) {
	f := newFakeFleets()
	h := f.host(true)
	if rec := fetch(h, "/f0000000000ff/"); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "fleet not found") {
		t.Fatalf("unknown fleet = %d %q", rec.Code, rec.Body.String())
	}
	fetch(h, "/"+fleetA+"/")
	f.mu.Lock()
	delete(f.homes, fleetA)
	f.mu.Unlock()
	rec := fetch(h, "/"+fleetA+"/")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "fleet not found") || strings.Contains(rec.Body.String(), "/homes/") {
		t.Fatalf("dead fleet = %d %q", rec.Code, rec.Body.String())
	}
	if _, closed := f.seen(); !slices.Equal(closed, []string{fleetA + " /homes/a"}) {
		t.Fatalf("the dead fleet's handler stays open: closed %q", closed)
	}
}

func TestTheFleetListIsLoopbackOnly(t *testing.T) {
	loop := fetch(newFakeFleets().host(true), "/")
	body := loop.Body.String()
	if loop.Code != http.StatusOK || !strings.Contains(body, `href="/`+fleetA+`/"`) || !strings.Contains(body, "work") || !strings.Contains(body, fleetB) {
		t.Fatalf("loopback list = %d\n%s", loop.Code, body)
	}
	if !strings.Contains(loop.Header().Get("Content-Security-Policy"), "default-src 'none'") || loop.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("list headers = %v", loop.Header())
	}
	net := fetch(newFakeFleets().host(false), "/")
	body = net.Body.String()
	if net.Code != http.StatusOK || strings.Contains(body, fleetA) || strings.Contains(body, "work") || !strings.Contains(body, "fleet's link") {
		t.Fatalf("network list = %d\n%s", net.Code, body)
	}
}

func TestTwoFleetsKeepSeparateLogins(t *testing.T) {
	tokenB := strings.Repeat("b", 32)
	stA, stB := open(t), open(t)
	h := board.NewHost(board.HostOptions{
		Loopback: true,
		Resolve: func(id string) (string, error) {
			if id != fleetA && id != fleetB {
				return "", state.ErrNotFound
			}
			return "/homes/" + id, nil
		},
		Open: func(id, _ string) (http.Handler, io.Closer, error) {
			st, tok := stA, token
			if id == fleetB {
				st, tok = stB, tokenB
			}
			return board.New(st, tok, board.Options{Base: "/" + id}), closer(func() error { return nil }), nil
		},
		List: func() ([]board.FleetLink, error) { return nil, nil },
	})
	login := fetch(h, "/"+fleetA+"/?token="+token)
	if login.Code != http.StatusSeeOther || login.Header().Get("Location") != "/"+fleetA+"/" || !strings.Contains(login.Header().Get("Set-Cookie"), "Path=/"+fleetA+"/") {
		t.Fatalf("login A = %d %q %q", login.Code, login.Header().Get("Location"), login.Header().Get("Set-Cookie"))
	}
	with := func(method, path, cookie string, form url.Values) int {
		var body io.Reader = strings.NewReader("")
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req := httptest.NewRequest(method, path, body)
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		req.AddCookie(&http.Cookie{Name: "hand_board", Value: cookie})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := with("GET", "/"+fleetA+"/", token, nil); code != http.StatusOK {
		t.Fatalf("A with its cookie = %d", code)
	}
	if code := with("GET", "/"+fleetB+"/", token, nil); code != http.StatusForbidden {
		t.Fatalf("B with A's cookie = %d", code)
	}
	if code := with("GET", "/"+fleetB+"/", tokenB, nil); code != http.StatusOK {
		t.Fatalf("B with its cookie = %d", code)
	}
	if code := with("POST", "/"+fleetB+"/report/r1/ack", tokenB, url.Values{"csrf": {token}}); code != http.StatusForbidden {
		t.Fatalf("B with A's csrf = %d", code)
	}
}
