package board_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/sidecar"
)

var (
	cookieValue = regexp.MustCompile(`^[1-9][0-9]*\.[0-9a-f]{64}$`)
)

func realSidecarBinary(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("HAND_IROH_BIN")
	if bin == "" {
		t.Skip("HAND_IROH_BIN is not set: build the sidecar with `cargo build --locked` in iroh/ and point HAND_IROH_BIN at target/debug/hand-iroh (the `[CI] Iroh` job does)")
	}
	abs, err := filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sidecar.Verify(context.Background(), abs); err != nil {
		t.Fatal(err)
	}
	return abs
}

func freeUDP(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	return pc.LocalAddr().String()
}

func eventuallyWithin(t *testing.T, what string, within time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type realHost struct {
	t           *testing.T
	bin, udp    string
	keys        string
	boardAddr   string
	secretFile  string
	side        *sidecar.Sidecar
	cancel      context.CancelFunc
	done        chan struct{}
	endpointID  string
	hostStopped bool
}

func newRealHost(t *testing.T, rig *peerRig) *realHost {
	t.Helper()
	dir := t.TempDir()
	h := &realHost{t: t, bin: realSidecarBinary(t), udp: freeUDP(t), keys: filepath.Join(dir, "host-keys"), boardAddr: rig.addr, secretFile: filepath.Join(dir, "board.peer")}
	if err := os.WriteFile(h.secretFile, []byte(peerSecret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.start()
	t.Cleanup(h.stop)
	return h
}

func (h *realHost) start() {
	h.t.Helper()
	h.side = sidecar.New(sidecar.Options{
		Resolve:    func(context.Context) (string, error) { return h.bin, nil },
		Board:      h.boardAddr,
		SecretFile: h.secretFile,
		KeyDir:     h.keys,
		Dir:        filepath.Dir(h.secretFile),
		Args:       []string{"--relay", "none", "--bind", h.udp},
		Attempts:   1,
		StopWait:   30 * time.Second,
	})
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel, h.done, h.hostStopped = cancel, make(chan struct{}), false
	go func() {
		defer close(h.done)
		h.side.Run(ctx)
	}()
	eventuallyWithin(h.t, "the host's sidecar to be ready", 30*time.Second, func() bool {
		if st := h.side.Status(); st.State == sidecar.Failed {
			h.t.Fatalf("the host's sidecar failed: %+v", st)
		}
		return h.side.Status().State == sidecar.Ready
	})
	h.endpointID = h.side.Status().ID
}

func (h *realHost) stop() {
	if h.hostStopped {
		return
	}
	h.hostStopped = true
	h.cancel()
	<-h.done
}

type realClient struct {
	t      *testing.T
	c      *sidecar.Client
	cancel context.CancelFunc
	done   chan struct{}
	http   *http.Client
	fleet  string
}

func (h *realHost) client(keys string, newKey bool, fleet string) *realClient {
	h.t.Helper()
	c, err := sidecar.NewClient(sidecar.ClientOptions{
		Resolve:   func(context.Context) (string, error) { return h.bin, nil },
		Host:      h.endpointID,
		KeyDir:    keys,
		Dir:       filepath.Dir(keys),
		NewKey:    newKey,
		Relay:     "none",
		Direct:    h.udp,
		StopWait:  30 * time.Second,
		ReadyWait: 30 * time.Second,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	rc := &realClient{t: h.t, c: c, cancel: cancel, done: make(chan struct{}), fleet: fleet,
		http: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true, MaxConnsPerHost: 6}}}
	go func() {
		defer close(rc.done)
		c.Run(ctx)
	}()
	h.t.Cleanup(rc.stop)
	eventuallyWithin(h.t, "the client to listen", 30*time.Second, func() bool {
		if st := c.Status(); st.State == sidecar.Failed {
			h.t.Fatalf("the client failed: %+v", st)
		}
		return c.Status().State == sidecar.Ready
	})
	return rc
}

func (rc *realClient) stop() {
	rc.cancel()
	<-rc.done
}

type realReply struct {
	Code   int
	Header http.Header
	Body   string
}

func (rc *realClient) try(method, path, body, cookie, csrf string) (realReply, error) {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, "http://"+rc.c.Status().Addr+"/"+rc.fleet+"/api/v1/"+path, rd)
	if err != nil {
		rc.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if csrf != "" {
		req.Header.Set("X-Hand-CSRF", csrf)
	}
	res, err := rc.http.Do(req)
	if err != nil {
		return realReply{}, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	return realReply{res.StatusCode, res.Header, string(b)}, err
}

func (rc *realClient) do(method, path, body, cookie, csrf string) realReply {
	rc.t.Helper()
	rep, err := rc.try(method, path, body, cookie, csrf)
	if err != nil {
		rc.t.Fatalf("%s %s: %v", method, path, err)
	}
	return rep
}

func (rc *realClient) pair(code string) (realReply, string) {
	rc.t.Helper()
	rep := rc.do("POST", "pair", pairBody(code, "Hand Desktop"), "", "")
	var got pairReply
	_ = json.Unmarshal([]byte(rep.Body), &got)
	if rep.Code != http.StatusOK {
		return rep, ""
	}
	if len(got.Data.Fleets) != 1 || got.Data.Fleets[0].ID != rc.fleet || got.Data.Fleets[0].CookieName != "hand_phone_"+rc.fleet || !cookieValue.MatchString(got.Data.Fleets[0].CookieValue) {
		rc.t.Fatalf("the pair answer has the wrong shape: %.200s", rep.Body)
	}
	return rep, got.Data.Fleets[0].CookieName + "=" + got.Data.Fleets[0].CookieValue
}

func (rc *realClient) csrf(cookie string) string {
	rc.t.Helper()
	rep := rc.do("GET", "session", "", cookie, "")
	var got struct{ Data struct{ CSRF string } }
	if err := json.Unmarshal([]byte(rep.Body), &got); err != nil || rep.Code != http.StatusOK || got.Data.CSRF == "" {
		rc.t.Fatalf("session = %d: %.200s", rep.Code, rep.Body)
	}
	return got.Data.CSRF
}

func errorCode(rep realReply) string {
	var got struct{ Error struct{ Code string } }
	_ = json.Unmarshal([]byte(rep.Body), &got)
	return got.Error.Code
}

func noLongerPaired(rep realReply) bool {
	msg, _ := url.PathUnescape(rep.Header.Get("X-Hand-Error"))
	return rep.Code == http.StatusForbidden && strings.Contains(msg, "no longer paired")
}

func TestRealClientPairsReadsAndPostsThroughTheHostsSidecar(t *testing.T) {
	rig := newPeerRig(t, "alpha")
	f := rig.fleets[0]
	var mu sync.Mutex
	var calls []string
	rig.mu.Lock()
	rig.control = func(_ context.Context, args ...string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, strings.Join(args, " "))
		return "", nil
	}
	rig.mu.Unlock()
	host := newRealHost(t, rig)
	keys := filepath.Join(t.TempDir(), "client-keys")
	rc := host.client(keys, true, f.id)
	if st := rc.c.Status(); !strings.HasPrefix(st.Addr, "127.0.0.1:") || st.ID == host.endpointID || len(st.ID) != 64 {
		t.Fatalf("client status = %+v", st)
	}
	if rep := rc.do("GET", "status", "", "", ""); rep.Code != http.StatusForbidden || !strings.Contains(rep.Body, "Pair this Companion first") {
		t.Fatalf("an unpaired read = %d: %.200s", rep.Code, rep.Body)
	}
	code := rig.code(f)
	rep, cookie := rc.pair(code)
	if cookie == "" {
		t.Fatalf("pair = %d: %.200s", rep.Code, rep.Body)
	}
	if rep := rc.do("GET", "status", "", cookie, ""); rep.Code != http.StatusOK || !strings.Contains(rep.Body, `"`+f.id+`"`) {
		t.Fatalf("a paired read = %d: %.200s", rep.Code, rep.Body)
	}
	csrf := rc.csrf(cookie)
	message := `{"text":"hello from the desktop client"}`
	if rep := rc.do("POST", "message", message, cookie, ""); rep.Code != http.StatusForbidden || errorCode(rep) != "csrf_required" {
		t.Fatalf("a post without the CSRF header = %d: %.200s", rep.Code, rep.Body)
	}
	if rep := rc.do("POST", "message", message, cookie, csrf); rep.Code != http.StatusOK {
		t.Fatalf("a post with the CSRF header = %d: %.200s", rep.Code, rep.Body)
	}
	mu.Lock()
	if len(calls) != 1 || !strings.Contains(calls[0], "hello from the desktop client") {
		t.Fatalf("the Board was asked %q, want the one message", calls)
	}
	mu.Unlock()
	if rep, again := rc.pair(code); again != "" || rep.Code != http.StatusForbidden || errorCode(rep) != "code_used" {
		t.Fatalf("a used code = %d: %.200s", rep.Code, rep.Body)
	}
	eventuallyWithin(t, "the client to say connected", 10*time.Second, func() bool { return rc.c.Status().Link == sidecar.Connected })
}

func TestRealClientIsToldWhenTheHostRevokesIt(t *testing.T) {
	rig := newPeerRig(t, "alpha")
	f := rig.fleets[0]
	host := newRealHost(t, rig)
	rc := host.client(filepath.Join(t.TempDir(), "client-keys"), true, f.id)
	_, cookie := rc.pair(rig.code(f))
	if rc.do("GET", "status", "", cookie, "").Code != http.StatusOK {
		t.Fatal("a paired read failed")
	}
	rig.revoke(f, &http.Cookie{Value: strings.SplitN(cookie, "=", 2)[1]})
	eventuallyWithin(t, "the revoked cookie to be refused", 20*time.Second, func() bool {
		rep, err := rc.try("GET", "status", "", cookie, "")
		return err == nil && noLongerPaired(rep)
	})
	rep := rc.do("GET", "status", "", cookie, "")
	if rep.Code != http.StatusForbidden || !strings.Contains(rep.Body, "no longer paired") || strings.HasPrefix(strings.TrimSpace(rep.Body), "{") {
		t.Fatalf("a revoked read = %d: %.200s", rep.Code, rep.Body)
	}
}

func TestRealClientWithAReplacedKeyIsAnotherDevice(t *testing.T) {
	rig := newPeerRig(t, "alpha")
	f := rig.fleets[0]
	host := newRealHost(t, rig)
	keys := filepath.Join(t.TempDir(), "client-keys")
	first := host.client(keys, true, f.id)
	firstID := first.c.Status().ID
	_, cookie := first.pair(rig.code(f))
	first.stop()
	if err := os.RemoveAll(keys); err != nil {
		t.Fatal(err)
	}
	second := host.client(keys, true, f.id)
	if second.c.Status().ID == firstID {
		t.Fatal("a new key kept the old endpoint ID")
	}
	rep := second.do("GET", "status", "", cookie, "")
	if rep.Code != http.StatusForbidden || !strings.Contains(rep.Body, "Pair this Companion first") || rep.Header.Get("X-Hand-Error") != "" {
		t.Fatalf("the old cookie from a new key = %d: %.200s", rep.Code, rep.Body)
	}
	if _, again := second.pair(rig.code(f)); again == "" || again == cookie {
		t.Fatal("the new key could not pair")
	}
}

func TestRealClientReconnectsWhenTheHostRestarts(t *testing.T) {
	rig := newPeerRig(t, "alpha")
	f := rig.fleets[0]
	host := newRealHost(t, rig)
	rc := host.client(filepath.Join(t.TempDir(), "client-keys"), true, f.id)
	_, cookie := rc.pair(rig.code(f))
	if rc.do("GET", "status", "", cookie, "").Code != http.StatusOK {
		t.Fatal("a paired read failed")
	}
	id := host.endpointID
	host.stop()
	host.start()
	if host.endpointID != id {
		t.Fatalf("the host's ID changed across a restart: %s then %s", id, host.endpointID)
	}
	eventuallyWithin(t, "a read after the host restarted", 60*time.Second, func() bool {
		rep, err := rc.try("GET", "status", "", cookie, "")
		return err == nil && rep.Code == http.StatusOK
	})
}

func TestRealClientSaysBoardDownWhenTheHostCannotReachItsBoard(t *testing.T) {
	rig := newPeerRig(t, "alpha")
	f := rig.fleets[0]
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	rig.addr = ln.Addr().String()
	_ = ln.Close()
	host := newRealHost(t, rig)
	rc := host.client(filepath.Join(t.TempDir(), "client-keys"), true, f.id)
	eventuallyWithin(t, "board_down", 30*time.Second, func() bool {
		_, _ = rc.try("GET", "status", "", "", "")
		return rc.c.Status().Link == sidecar.BoardDown
	})
}
