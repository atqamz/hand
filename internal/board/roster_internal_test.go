package board

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

func labelled(t *testing.T, st *state.Store, name, label string) {
	t.Helper()
	if _, err := st.AddDevice(context.Background(), name, "", strings.Repeat("a", 64), label); err != nil {
		t.Fatal(err)
	}
}

func TestARevocationRevokesOnlyInTheFleetItNamesAndOnlyAValidLabel(t *testing.T) {
	ctx := context.Background()
	a, sa, aid := pairBoard(t, strings.Repeat("1", 32))
	_, sb, bid := pairBoard(t, strings.Repeat("2", 32))
	label := "0123456789abcdef"
	for _, st := range []*state.Store{sa, sb} {
		labelled(t, st, "Pixel", label)
		labelled(t, st, "Old", "")
	}
	for what, rv := range map[string]*Revocation{
		"nil":               nil,
		"empty":             {},
		"another fleet":     {fleet: bid, label: label},
		"no fleet":          {label: label},
		"an unknown fleet":  {fleet: "f000000000000", label: label},
		"no label":          {fleet: aid},
		"a wildcard":        {fleet: aid, label: "%"},
		"a short label":     {fleet: aid, label: "0123456789abcde"},
		"an upper-case one": {fleet: aid, label: "0123456789ABCDEF"},
	} {
		if n, err := a.RevokePhone(ctx, rv); err == nil || n != 0 {
			t.Errorf("%s = %d, %v", what, n, err)
		}
	}
	for _, st := range []*state.Store{sa, sb} {
		if list, _ := st.Devices(ctx); len(list) != 2 {
			t.Fatalf("a refused revocation left %v", list)
		}
	}
	if n, err := a.RevokePhone(ctx, &Revocation{fleet: aid, label: label}); err != nil || n != 1 {
		t.Fatalf("the right revocation = %d, %v", n, err)
	}
	if list, _ := sa.Devices(ctx); len(list) != 1 || list[0].Name != "Old" {
		t.Fatalf("alpha holds %v", list)
	}
	if list, _ := sb.Devices(ctx); len(list) != 2 {
		t.Fatalf("beta holds %v", list)
	}
}

type rosterStub struct {
	mu      sync.Mutex
	opened  int
	listed  []string
	revoked []Revocation
}

func (s *rosterStub) ServeHTTP(http.ResponseWriter, *http.Request) {}

func (s *rosterStub) PhoneRecords(context.Context) ([]PhoneRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listed = append(s.listed, "listed")
	return nil, nil
}

func (s *rosterStub) RevokePhone(_ context.Context, rv *Revocation) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked = append(s.revoked, *rv)
	return 1, nil
}

func TestTheHostChecksTheLaptopsLoginAgainForEveryFleetAndEveryCall(t *testing.T) {
	const here, there, other = "f000000000001", "f000000000002", "f000000000003"
	good := strings.Repeat("a", 32)
	stub := &rosterStub{}
	opened := map[string]int{}
	var mu sync.Mutex
	newHost := func(valid func(id, token string) bool) *Host {
		return NewHost(HostOptions{
			Loopback: true,
			Resolve:  func(id string) (string, error) { return "/homes/" + id, nil },
			Open: func(id, home string) (http.Handler, io.Closer, error) {
				mu.Lock()
				opened[id]++
				mu.Unlock()
				return stub, closeFunc(func() error { return nil }), nil
			},
			List:  func() ([]FleetLink, error) { return nil, nil },
			Valid: valid,
		})
	}
	valid := func(id, token string) bool { return id == there && token == good }
	request := func(cookies ...*http.Cookie) *http.Request {
		r := httptest.NewRequest("POST", "/"+here+"/connect/revoke/everywhere", nil)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		return r
	}
	login := func(id, token string) *http.Cookie { return &http.Cookie{Name: cookieName + "_" + id, Value: token} }
	cases := []struct {
		what   string
		host   *Host
		cookie []*http.Cookie
		fleet  string
		reach  bool
	}{
		{"no login", newHost(valid), nil, there, false},
		{"a login with the wrong token", newHost(valid), []*http.Cookie{login(there, strings.Repeat("b", 32))}, there, false},
		{"a login for another fleet", newHost(valid), []*http.Cookie{login(other, good)}, there, false},
		{"the right token under another fleet's name", newHost(valid), []*http.Cookie{login(other, good)}, other, false},
		{"the current fleet", newHost(func(string, string) bool { return true }), []*http.Cookie{login(here, good)}, here, false},
		{"an id that is not a fleet", newHost(func(string, string) bool { return true }), []*http.Cookie{login("../"+there, good)}, "../" + there, false},
		{"an empty id", newHost(func(string, string) bool { return true }), nil, "", false},
		{"a host that cannot check a login", newHost(nil), []*http.Cookie{login(there, good)}, there, false},
		{"a stale login before a good one", newHost(valid), []*http.Cookie{login(there, strings.Repeat("b", 32)), login(there, good)}, there, true},
		{"the right login", newHost(valid), []*http.Cookie{login(there, good)}, there, true},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			stub.mu.Lock()
			stub.listed, stub.revoked = nil, nil
			stub.mu.Unlock()
			mu.Lock()
			clear(opened)
			mu.Unlock()
			r := request(c.cookie...)
			_, listErr := c.host.listIn(here, r)(context.Background(), c.fleet)
			n, revokeErr := c.host.revokeIn(here, r)(context.Background(), c.fleet, "0123456789abcdef")
			stub.mu.Lock()
			defer stub.mu.Unlock()
			if c.reach {
				if listErr != nil || revokeErr != nil || n != 1 || len(stub.listed) != 1 || len(stub.revoked) != 1 {
					t.Fatalf("the proven fleet was not reached: %v, %v, %d, %v, %v", listErr, revokeErr, n, stub.listed, stub.revoked)
				}
				if got := stub.revoked[0]; got.fleet != c.fleet || got.label != "0123456789abcdef" {
					t.Fatalf("the revocation names %+v", got)
				}
				return
			}
			if !errors.Is(listErr, errNotProven) || !errors.Is(revokeErr, errNotProven) || n != 0 {
				t.Fatalf("an unproven fleet = %v, %v, %d", listErr, revokeErr, n)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(stub.listed)+len(stub.revoked) != 0 || len(opened) != 0 {
				t.Fatalf("an unproven fleet was opened %v and reached %v %v", opened, stub.listed, stub.revoked)
			}
		})
	}
}

type closeFunc func() error

func (c closeFunc) Close() error { return c() }

func TestAHandlerWithoutTheMethodsIsNeverTakenForAFleetItCanReach(t *testing.T) {
	const here, there = "f000000000001", "f000000000002"
	bare := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	h := NewHost(HostOptions{
		Loopback: true,
		Resolve:  func(id string) (string, error) { return "/homes/" + id, nil },
		Open: func(id, home string) (http.Handler, io.Closer, error) {
			return bare, closeFunc(func() error { return nil }), nil
		},
		List:  func() ([]FleetLink, error) { return nil, nil },
		Valid: func(string, string) bool { return true },
	})
	r := httptest.NewRequest("POST", "/"+here+"/", nil)
	r.AddCookie(&http.Cookie{Name: cookieName + "_" + there, Value: "x"})
	if _, err := h.listIn(here, r)(context.Background(), there); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("a handler with no PhoneRecords = %v", err)
	}
	if n, err := h.revokeIn(here, r)(context.Background(), there, "0123456789abcdef"); !errors.Is(err, state.ErrNotFound) || n != 0 {
		t.Fatalf("a handler with no RevokePhone = %d, %v", n, err)
	}
}

func TestEveryFleetOfARevokeEverywhereGetsItsOwnDeadline(t *testing.T) {
	const token = "00000000000000000000000000000001"
	b, st, id := pairBoard(t, token)
	label := "0123456789abcdef"
	labelled(t, st, "Pixel", label)
	others := []FleetLink{{ID: id, Name: "alpha"}, {ID: "f000000000002", Name: "beta"}, {ID: "f000000000003", Name: "gamma"}}
	var mu sync.Mutex
	deadlines := map[string]time.Time{}
	revoke := revokeFunc(func(ctx context.Context, fleet, label string) (int, error) {
		d, ok := ctx.Deadline()
		if !ok {
			return 0, errors.New("a revoke with no deadline")
		}
		mu.Lock()
		deadlines[fleet] = d
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		return 0, nil
	})
	r := httptest.NewRequest("POST", "/connect/revoke/everywhere", strings.NewReader(url.Values{"csrf": {token}, "phone": {label}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: cookieFor("/" + id), Value: token})
	ctx := context.WithValue(r.Context(), laptopKey{}, func() []FleetLink { return others })
	ctx = context.WithValue(ctx, revokeKey{}, revoke)
	rec := httptest.NewRecorder()
	b.ServeHTTP(rec, r.WithContext(ctx))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("revoke everywhere = %d:\n%s", rec.Code, rec.Body.String())
	}
	if len(deadlines) != 2 || !deadlines["f000000000003"].After(deadlines["f000000000002"]) {
		t.Fatalf("the fleets share one deadline: %v", deadlines)
	}
	if list, _ := st.Devices(context.Background()); len(list) != 0 {
		t.Fatalf("the current fleet still holds %v", list)
	}
}
