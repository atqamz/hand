package board_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

var phoneEpoch = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

type phoneHost struct {
	t        *testing.T
	loopback bool
	fleets   []railFleet
	h        *board.Host
	mu       sync.Mutex
	now      time.Time
	opened   map[string]int
	proofs   map[string]int
}

type counted struct {
	http.Handler
	p  *phoneHost
	id string
}

func (c counted) PhoneSession(r *http.Request) bool {
	c.p.mu.Lock()
	c.p.proofs[c.id]++
	c.p.mu.Unlock()
	return c.Handler.(board.Prover).PhoneSession(r)
}

func (p *phoneHost) clock() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.now
}

func (p *phoneHost) advance(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.now = p.now.Add(d)
}

func newPhoneHost(t *testing.T, loopback bool, names ...string) *phoneHost {
	t.Helper()
	p := &phoneHost{t: t, loopback: loopback, now: phoneEpoch}
	for i, name := range names {
		st, err := state.Open(filepath.Join(t.TempDir(), "hand.db"), p.clock)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		if _, err := st.CreateFleet(context.Background(), "test"); err != nil {
			t.Fatal(err)
		}
		f, err := st.RenameFleet(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		p.fleets = append(p.fleets, railFleet{id: f.ID, name: name, token: fmt.Sprintf("%032x", i+1), st: st})
	}
	p.restart()
	return p
}

func (p *phoneHost) restart() {
	p.mu.Lock()
	p.opened, p.proofs = map[string]int{}, map[string]int{}
	p.mu.Unlock()
	byID := map[string]railFleet{}
	var all []board.FleetLink
	for _, f := range p.fleets {
		byID[f.id] = f
		all = append(all, board.FleetLink{ID: f.id, Name: f.name})
	}
	p.h = board.NewHost(board.HostOptions{
		Loopback: p.loopback,
		Resolve: func(id string) (string, error) {
			if _, ok := byID[id]; !ok {
				return "", state.ErrNotFound
			}
			return "/homes/" + id, nil
		},
		Open: func(id, _ string) (http.Handler, io.Closer, error) {
			f := byID[id]
			p.mu.Lock()
			p.opened[id]++
			p.mu.Unlock()
			b := board.New(f.st, f.token, board.Options{Base: "/" + id, Controls: p.loopback, Now: p.clock})
			return counted{b, p, id}, closer(func() error { return nil }), nil
		},
		List:  func() ([]board.FleetLink, error) { return all, nil },
		Valid: func(id, token string) bool { f, ok := byID[id]; return ok && f.token == token },
	})
}

func (p *phoneHost) send(method, path string, form url.Values, cookies []*http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	p.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	p.h.ServeHTTP(rec, req)
	return rec
}

func serving(login string) map[string]string {
	return map[string]string{"X-Forwarded-For": "100.64.0.7", "X-Forwarded-Proto": "https", "Tailscale-User-Login": login}
}

func (p *phoneHost) laptop(f railFleet) *http.Cookie {
	return &http.Cookie{Name: "hand_board_" + f.id, Value: f.token}
}

func (p *phoneHost) pair(f railFleet, headers map[string]string) *http.Cookie {
	p.t.Helper()
	rec := p.send("POST", "/"+f.id+"/connect/code", url.Values{"csrf": {f.token}, "base": {"https://laptop.example"}}, []*http.Cookie{p.laptop(f)}, nil)
	m := codeRe.FindStringSubmatch(rec.Body.String())
	if m == nil {
		p.t.Fatalf("no code for %s = %d:\n%s", f.name, rec.Code, rec.Body.String())
	}
	rec = p.send("POST", "/"+f.id+"/pair", url.Values{"code": {m[1]}, "name": {"Pixel"}}, nil, headers)
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge > 0 {
			cookie = c
		}
	}
	if rec.Code != http.StatusSeeOther || cookie == nil {
		p.t.Fatalf("pair %s = %d:\n%s", f.name, rec.Code, rec.Body.String())
	}
	return cookie
}

func (p *phoneHost) revoke(f railFleet, phone *http.Cookie) {
	p.t.Helper()
	id := strings.SplitN(phone.Value, ".", 2)[0]
	rec := p.send("POST", "/"+f.id+"/connect/revoke", url.Values{"csrf": {f.token}, "id": {id}}, []*http.Cookie{p.laptop(f)}, nil)
	if rec.Code != http.StatusSeeOther {
		p.t.Fatalf("revoke %s = %d", f.name, rec.Code)
	}
}

func phoneNamed(name string, c *http.Cookie) *http.Cookie {
	return &http.Cookie{Name: name, Value: c.Value}
}
