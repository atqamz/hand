package desktop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/remote"
	"github.com/atqamz/hand/internal/sidecar"
)

const (
	pairCode = "K7QM3XRD2P"
	pairLink = "handboard://c#v=1&e=" + rtHostA + "&f=" + rtA + "&c=" + pairCode + "&n=Lab"
)

type pairClients struct {
	mu     sync.Mutex
	st     sidecar.ClientStatus
	starts []bool
	stops  []string
	fail   error
}

func (c *pairClients) Start(_ remote.Host, newKey bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.starts = append(c.starts, newKey)
	return c.fail
}

func (c *pairClients) Status(string) sidecar.ClientStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.st
}

func (c *pairClients) Stop(host string) {
	c.mu.Lock()
	c.stops = append(c.stops, host)
	c.mu.Unlock()
}

type pairPort struct {
	mu     sync.Mutex
	reqs   []string
	body   []byte
	cookie string
	status int
	answer string
}

func (p *pairPort) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	p.mu.Lock()
	p.reqs = append(p.reqs, r.Method+" "+r.URL.Path)
	p.body, p.cookie = raw, r.Header.Get("Cookie")+r.Header.Get("X-Hand-CSRF")
	status, answer := p.status, p.answer
	p.mu.Unlock()
	w.WriteHeader(status)
	io.WriteString(w, answer)
}

func pairedAnswer(fleets ...string) string {
	var parts []string
	for i, id := range fleets {
		parts = append(parts, fmt.Sprintf(`{"id":%q,"name":"fleet %d","cookie_name":"hand_phone_%s","cookie_value":%q}`, id, i, id, rtCookieA))
	}
	return `{"version":1,"data":{"fleets":[` + strings.Join(parts, ",") + `],"missed":0}}`
}

type pairRig struct {
	port    *pairPort
	clients *pairClients
	store   *remote.Store
	remotes *desktop.Remotes
}

func newPairRig(t *testing.T, fleets ...string) *pairRig {
	t.Helper()
	port := &pairPort{status: 200, answer: pairedAnswer(fleets...)}
	srv := httptest.NewServer(port)
	t.Cleanup(srv.Close)
	clients := &pairClients{st: sidecar.ClientStatus{State: sidecar.Ready, Addr: strings.TrimPrefix(srv.URL, "http://"), ID: rtClient}}
	store := remote.OpenStore(filepath.Join(t.TempDir(), "remote"))
	return &pairRig{port: port, clients: clients, store: store, remotes: &desktop.Remotes{
		Store: store, Clients: clients, Ready: time.Second,
		Now: func() time.Time { return time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC) },
	}}
}

func parseLink(t *testing.T, text string) remote.Link {
	t.Helper()
	l, err := remote.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func (g *pairRig) pair(t *testing.T, req desktop.PairRequest) error {
	t.Helper()
	if req.Link.Host == "" {
		req.Link = parseLink(t, pairLink)
	}
	if req.Name == "" {
		req.Name = "Hand Desktop"
	}
	return g.remotes.Pair(context.Background(), req)
}

func TestPairingStartsAFreshKeyRedeemsOnTheFleetsPathAndStoresTheDevice(t *testing.T) {
	g := newPairRig(t, rtA)
	if err := g.pair(t, desktop.PairRequest{Name: "Lab desk"}); err != nil {
		t.Fatal(err)
	}
	if len(g.clients.starts) != 1 || !g.clients.starts[0] {
		t.Fatalf("starts = %v, want one with a new key", g.clients.starts)
	}
	if got := g.port.reqs; len(got) != 1 || got[0] != "POST /"+rtA+"/api/v1/pair" {
		t.Fatalf("requests = %v", got)
	}
	var body map[string]string
	if err := json.Unmarshal(g.port.body, &body); err != nil || len(body) != 2 || body["code"] != pairCode || body["name"] != "Lab desk" {
		t.Fatalf("body = %s, %v", g.port.body, err)
	}
	if g.port.cookie != "" {
		t.Fatalf("the pair request carried %q", g.port.cookie)
	}
	hosts, err := g.store.Hosts()
	if err != nil || len(hosts) != 1 {
		t.Fatalf("hosts = %v, %v", hosts, err)
	}
	h := hosts[0]
	if h.ID != rtHostA || h.ClientID != rtClient || h.Label != "" || h.PairedAt.Format("2006-01-02") != "2026-11-18" || len(h.Fleets) != 1 || h.Fleets[0].ID != rtA {
		t.Fatalf("host = %+v", h)
	}
	if c, err := g.store.Cookie(rtHostA, rtA); err != nil || c != rtCookieA {
		t.Fatalf("cookie = %q, %v", c, err)
	}
}

func TestPairingAKnownHostKeepsItsKey(t *testing.T) {
	g := newPairRig(t, rtB)
	if err := g.store.Add(remote.Host{ID: rtHostA, ClientID: rtClient, PairedAt: time.Now()}, remote.Fleet{ID: rtA, Name: "a"}, rtCookieA, nil); err != nil {
		t.Fatal(err)
	}
	link := parseLink(t, "handboard://c#v=1&e="+rtHostA+"&f="+rtB+"&c="+pairCode)
	if err := g.pair(t, desktop.PairRequest{Link: link}); err != nil {
		t.Fatal(err)
	}
	if len(g.clients.starts) != 1 || g.clients.starts[0] {
		t.Fatalf("starts = %v, want one without a new key", g.clients.starts)
	}
	if hosts, _ := g.store.Hosts(); len(hosts) != 1 || len(hosts[0].Fleets) != 2 {
		t.Fatalf("hosts = %+v", hosts)
	}
}

func TestAFleetTheWindowHoldsIsRefusedBeforeAnyStartOrDial(t *testing.T) {
	g := newPairRig(t, rtA)
	err := g.pair(t, desktop.PairRequest{Held: func(id string) bool { return id == rtA }})
	var pe *desktop.PairError
	if !errors.As(err, &pe) || pe.Code != "fleet_held" || !strings.Contains(err.Error(), "already holds") {
		t.Fatalf("err = %v", err)
	}
	if len(g.clients.starts) != 0 || len(g.port.reqs) != 0 {
		t.Fatalf("started %v, dialled %v", g.clients.starts, g.port.reqs)
	}
}

func TestEachRefusalOfTheCodeReadsAsItsOwnSentenceAndForgetsANewHost(t *testing.T) {
	cases := []struct {
		status int
		code   string
		want   string
	}{
		{403, "wrong_code", "That code is not right."},
		{403, "code_expired", "That code has expired."},
		{403, "code_used", "That code was already used."},
		{403, "codes_cancelled", "Every code was cancelled."},
		{429, "rate_limited", "Wait a minute"},
		{429, "", "Wait a minute"},
		{403, "", "The host refused the code."},
	}
	for _, c := range cases {
		t.Run(fmt.Sprint(c.status, c.code), func(t *testing.T) {
			g := newPairRig(t, rtA)
			g.port.status = c.status
			g.port.answer = fmt.Sprintf(`{"error":{"status":%d,"code":%q,"message":"host text %s"}}`, c.status, c.code, pairCode)
			err := g.pair(t, desktop.PairRequest{})
			if err == nil || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), "host text") || strings.Contains(err.Error(), pairCode) {
				t.Fatalf("err = %v", err)
			}
			if hosts, _ := g.store.Hosts(); len(hosts) != 0 {
				t.Fatalf("a failed pairing left %+v", hosts)
			}
			if len(g.clients.stops) != 1 || g.clients.stops[0] != rtHostA {
				t.Fatalf("stops = %v", g.clients.stops)
			}
		})
	}
}

func TestAFailedPairingNeverStopsOrForgetsAHostItAlreadyHeld(t *testing.T) {
	g := newPairRig(t, rtB)
	g.port.status, g.port.answer = 403, `{"error":{"status":403,"code":"code_used","message":"x"}}`
	if err := g.store.Add(remote.Host{ID: rtHostA, ClientID: rtClient, PairedAt: time.Now()}, remote.Fleet{ID: rtA, Name: "a"}, rtCookieA, nil); err != nil {
		t.Fatal(err)
	}
	link := parseLink(t, "handboard://c#v=1&e="+rtHostA+"&f="+rtB+"&c="+pairCode)
	if err := g.pair(t, desktop.PairRequest{Link: link}); err == nil {
		t.Fatal("no error")
	}
	if len(g.clients.stops) != 0 {
		t.Fatalf("stops = %v", g.clients.stops)
	}
	if hosts, _ := g.store.Hosts(); len(hosts) != 1 {
		t.Fatalf("hosts = %+v", hosts)
	}
}

func TestACodeThatNamesSeveralFleetsStoresTheOnesNotHeld(t *testing.T) {
	g := newPairRig(t, rtA, rtB)
	if err := g.pair(t, desktop.PairRequest{Held: func(id string) bool { return id == rtB }}); err != nil {
		t.Fatal(err)
	}
	hosts, _ := g.store.Hosts()
	if len(hosts) != 1 || len(hosts[0].Fleets) != 1 || hosts[0].Fleets[0].ID != rtA {
		t.Fatalf("hosts = %+v", hosts)
	}
}

func TestAnAnswerThatDoesNotNameTheLinkedFleetOrItsCookieIsRefused(t *testing.T) {
	for name, answer := range map[string]string{
		"other fleet":  pairedAnswer(rtB),
		"cookie name":  strings.Replace(pairedAnswer(rtA), "hand_phone_"+rtA, "hand_board_"+rtA, 1),
		"not json":     "nope",
		"cookie shape": strings.Replace(pairedAnswer(rtA), rtCookieA, "short", 1),
	} {
		t.Run(name, func(t *testing.T) {
			g := newPairRig(t, rtA)
			g.port.answer = answer
			if err := g.pair(t, desktop.PairRequest{}); err == nil {
				t.Fatal("no error")
			}
			if hosts, _ := g.store.Hosts(); len(hosts) != 0 {
				t.Fatalf("hosts = %+v", hosts)
			}
		})
	}
}

func TestAClientThatCannotStartOrNeverListensIsAnErrorNotAHang(t *testing.T) {
	g := newPairRig(t, rtA)
	g.clients.st = sidecar.ClientStatus{State: sidecar.Failed, Code: 3, Reason: "no key"}
	if err := g.pair(t, desktop.PairRequest{}); err == nil || !strings.Contains(err.Error(), "no key") {
		t.Fatalf("err = %v", err)
	}
	g = newPairRig(t, rtA)
	g.clients.st = sidecar.ClientStatus{State: sidecar.Starting}
	g.remotes.Ready = 80 * time.Millisecond
	err := g.pair(t, desktop.PairRequest{})
	var pe *desktop.PairError
	if !errors.As(err, &pe) || !pe.Transient || !strings.Contains(err.Error(), "Can't reach host") {
		t.Fatalf("err = %v", err)
	}
	g = newPairRig(t, rtA)
	g.clients.fail = errors.New("hand-iroh is missing")
	if err := g.pair(t, desktop.PairRequest{}); err == nil || !strings.Contains(err.Error(), "hand-iroh is missing") {
		t.Fatalf("err = %v", err)
	}
}

func TestAHostThatDropsTheConnectionReadsFromTheClientsLinkState(t *testing.T) {
	g := newPairRig(t, rtA)
	g.clients.st.Link = sidecar.BoardDown
	g.port.status = 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, _ := w.(http.Hijacker)
		c, _, _ := hj.Hijack()
		c.Close()
	}))
	defer srv.Close()
	g.clients.st.Addr = strings.TrimPrefix(srv.URL, "http://")
	err := g.pair(t, desktop.PairRequest{})
	var pe *desktop.PairError
	if !errors.As(err, &pe) || !pe.Transient || !strings.Contains(err.Error(), "Board not running on the host. Start it there.") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), pairCode) || strings.Contains(err.Error(), g.clients.st.Addr) {
		t.Fatalf("err = %v", err)
	}
}

func TestPairingAgainReplacesTheCookieClientIDAndDate(t *testing.T) {
	g := newPairRig(t, rtA)
	old := remote.Host{ID: rtHostA, ClientID: rtOther, PairedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}
	if err := g.store.Add(old, remote.Fleet{ID: rtA, Name: "a"}, rtCookieB, nil); err != nil {
		t.Fatal(err)
	}
	held := func(id string) bool { return id == rtA }
	if err := g.pair(t, desktop.PairRequest{Held: held}); err == nil {
		t.Fatal("a fleet held was paired without Again")
	}
	if err := g.pair(t, desktop.PairRequest{Held: held, Again: true}); err != nil {
		t.Fatal(err)
	}
	hosts, _ := g.store.Hosts()
	if len(hosts) != 1 || len(hosts[0].Fleets) != 1 || hosts[0].ClientID != rtClient || hosts[0].PairedAt.Year() != 2026 || hosts[0].PairedAt.Month() != 11 {
		t.Fatalf("hosts = %+v", hosts)
	}
	if c, _ := g.store.Cookie(rtHostA, rtA); c != rtCookieA {
		t.Fatalf("cookie = %q", c)
	}
}

func TestPairingAgainWithAnotherHostIsRefusedBeforeAnyStart(t *testing.T) {
	g := newPairRig(t, rtA)
	err := g.pair(t, desktop.PairRequest{Again: true})
	var pe *desktop.PairError
	if !errors.As(err, &pe) || pe.Code != "other_host" || len(g.clients.starts) != 0 {
		t.Fatalf("err = %v, starts %v", err, g.clients.starts)
	}
}

func TestPairingAgainWithAnotherFleetOfTheSameHostIsRefusedBeforeAnyStart(t *testing.T) {
	g := newPairRig(t, rtA)
	old := remote.Host{ID: rtHostA, ClientID: rtOther, PairedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}
	if err := g.store.Add(old, remote.Fleet{ID: rtB, Name: "b"}, rtCookieB, nil); err != nil {
		t.Fatal(err)
	}
	err := g.pair(t, desktop.PairRequest{Again: true})
	var pe *desktop.PairError
	if !errors.As(err, &pe) || pe.Code != "other_fleet" || len(g.clients.starts) != 0 {
		t.Fatalf("err = %v, starts %v", err, g.clients.starts)
	}
	hosts, _ := g.store.Hosts()
	if len(hosts) != 1 || len(hosts[0].Fleets) != 1 || hosts[0].Fleets[0].ID != rtB {
		t.Fatalf("hosts = %+v", hosts)
	}
}

func TestForgettingStopsTheClientAndDeletesTheHostsFolder(t *testing.T) {
	g := newPairRig(t, rtA)
	if err := g.pair(t, desktop.PairRequest{}); err != nil {
		t.Fatal(err)
	}
	dir, err := g.store.KeyDir(rtHostA)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hand-iroh.key"), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := g.remotes.Forget(rtHostA); err != nil {
		t.Fatal(err)
	}
	if len(g.clients.stops) != 1 || g.clients.stops[0] != rtHostA {
		t.Fatalf("stops = %v", g.clients.stops)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the host's folder is still there: %v", err)
	}
	if hosts, _ := g.store.Hosts(); len(hosts) != 0 {
		t.Fatalf("hosts = %+v", hosts)
	}
	if c, _ := g.store.Cookie(rtHostA, rtA); c != "" {
		t.Fatalf("cookie = %q", c)
	}
}

func TestNoLinkCodeOrCookieReachesAnyErrorLogOrStoredHostsFile(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	secrets := []string{pairCode, pairLink, rtCookieA, "Lab"}
	var all []string
	for _, c := range []struct {
		status int
		answer string
	}{
		{403, `{"error":{"code":"wrong_code","message":"` + pairCode + `"}}`},
		{500, pairCode},
		{200, "nope " + rtCookieA},
	} {
		g := newPairRig(t, rtA)
		g.port.status, g.port.answer = c.status, c.answer
		err := g.pair(t, desktop.PairRequest{})
		if err == nil {
			t.Fatal("no error")
		}
		all = append(all, err.Error(), fmt.Sprintf("%v %+v %#v", parseLink(t, pairLink), parseLink(t, pairLink), parseLink(t, pairLink)))
	}
	g := newPairRig(t, rtA)
	if err := g.pair(t, desktop.PairRequest{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(g.storeDir(), "hosts.json"))
	all = append(all, logs.String(), string(raw))
	for _, text := range all {
		for _, s := range secrets {
			if s != "Lab" && strings.Contains(text, s) {
				t.Fatalf("%q leaked into %q", s, text)
			}
		}
	}
	if strings.Contains(string(raw), "Lab") {
		t.Fatalf("the link's claimed name was stored: %s", raw)
	}
}

func (g *pairRig) storeDir() string {
	dir, _ := g.store.KeyDir(rtHostA)
	return filepath.Dir(dir)
}

func TestPairingAnotherFleetOnAKnownHostAfterAKeyChangeKeepsTheCurrentClientID(t *testing.T) {
	g := newPairRig(t, rtA)
	old := remote.Host{ID: rtHostA, ClientID: rtOther, PairedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}
	if err := g.store.Add(old, remote.Fleet{ID: rtB, Name: "b"}, rtCookieB, nil); err != nil {
		t.Fatal(err)
	}
	if err := g.pair(t, desktop.PairRequest{}); err != nil {
		t.Fatal(err)
	}
	hosts, _ := g.store.Hosts()
	if len(hosts) != 1 || len(hosts[0].Fleets) != 2 || hosts[0].ClientID != rtClient || hosts[0].PairedAt.Year() != 2026 || hosts[0].PairedAt.Month() != 1 {
		t.Fatalf("hosts = %+v", hosts)
	}
	if c, _ := g.store.Cookie(rtHostA, rtB); c != rtCookieB {
		t.Fatalf("the first fleet's cookie = %q", c)
	}
}
