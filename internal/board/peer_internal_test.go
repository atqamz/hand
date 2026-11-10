package board

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func peerHost() *Host {
	return NewHost(HostOptions{Loopback: true, Resolve: func(string) (string, error) { return "", errors.New("none") }})
}

func TestThePeerServerBoundsHowLongAConnectionMayTakeToSendItsHead(t *testing.T) {
	if d := NewPeer(peerHost(), "s").srv.ReadHeaderTimeout; d <= 0 || d > 30*time.Second {
		t.Fatalf("ReadHeaderTimeout = %v", d)
	}
	if n := NewPeer(peerHost(), "s").srv.MaxHeaderBytes; n <= 0 || n > 64<<10 {
		t.Fatalf("MaxHeaderBytes = %d", n)
	}
	if peerIdleWait <= 0 || peerIdleWait > time.Minute || peerBodyWait <= 0 || peerBodyWait > 30*time.Second {
		t.Fatalf("idle %v, body %v", peerIdleWait, peerBodyWait)
	}
}

func TestAConnectionThatStallsInsideAPrefaceIsClosedByTheHeaderTimeout(t *testing.T) {
	for name, head := range map[string]string{"half a preface": "HAND-PEER 1 " + peerIDOf('a'), "half a request": "GET / HT"} {
		p := NewPeer(peerHost(), "secret")
		p.srv.ReadHeaderTimeout = 150 * time.Millisecond
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go func() { _ = p.Serve(ln) }()
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(conn, head); err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err := io.Copy(io.Discard, conn); err != nil {
			t.Fatalf("%s: the board kept the stalled connection open: %v", name, err)
		}
		_ = conn.Close()
		_ = p.Shutdown(context.Background())
	}
}

func peerIDOf(c byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

func probeHost(read func(r *http.Request)) *Host {
	return NewHost(HostOptions{
		Loopback: true,
		Resolve:  func(string) (string, error) { return "/home", nil },
		Open: func(id, _ string) (http.Handler, io.Closer, error) {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				read(r)
				_, _ = io.WriteString(w, "ok")
			}), io.NopCloser(nil), nil
		},
	})
}

func TestAnUnpairedRequestThatStallsInsideItsBodyIsCutByTheBodyDeadline(t *testing.T) {
	old := peerBodyWait
	peerBodyWait = 200 * time.Millisecond
	t.Cleanup(func() { peerBodyWait = old })
	done := make(chan error, 1)
	p := NewPeer(probeHost(func(r *http.Request) { done <- r.ParseForm() }), "secret")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = p.Serve(ln) }()
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "POST /f0123456789ab/pair HTTP/1.1\r\nHost: x\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: 100\r\n\r\ncode="); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the form was read although its body never finished")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a stalled body held the handler past the body deadline")
	}
}

func TestTheBodyDeadlineDoesNotKillAnIdleKeepAliveConnection(t *testing.T) {
	old := peerBodyWait
	peerBodyWait = 100 * time.Millisecond
	t.Cleanup(func() { peerBodyWait = old })
	p := NewPeer(probeHost(func(*http.Request) {}), "secret")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = p.Serve(ln) }()
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	br := bufio.NewReader(conn)
	get := func() int {
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		if _, err := io.WriteString(conn, "GET /f0123456789ab/pair HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
			t.Fatal(err)
		}
		res, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		return res.StatusCode
	}
	if get() != http.StatusOK {
		t.Fatal("first request")
	}
	time.Sleep(400 * time.Millisecond)
	if get() != http.StatusOK {
		t.Fatal("the second request on the idle connection")
	}
}

func servePeer(t *testing.T, h *Host) string {
	t.Helper()
	p := NewPeer(h, "secret")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = p.Serve(ln) }()
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	return ln.Addr().String()
}

func TestAnIdleKeepAliveConnectionIsClosedByTheIdleTimeout(t *testing.T) {
	old := peerIdleWait
	peerIdleWait = 150 * time.Millisecond
	t.Cleanup(func() { peerIdleWait = old })
	conn, err := net.Dial("tcp", servePeer(t, probeHost(func(*http.Request) {})))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	br := bufio.NewReader(conn)
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(conn, "GET /f0123456789ab/pair HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(br, nil)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("first request = %v, %v", res, err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if _, err := br.ReadByte(); err != io.EOF {
		t.Fatalf("the idle connection was not closed by the server: %v", err)
	}
}

func TestAStalledBodyOnARefusedPathIsCutByTheBodyDeadline(t *testing.T) {
	old := peerBodyWait
	peerBodyWait = 200 * time.Millisecond
	t.Cleanup(func() { peerBodyWait = old })
	conn, err := net.Dial("tcp", servePeer(t, peerHost()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(conn, "POST /f0123456789ab/supervisor/send HTTP/1.1\r\nHost: x\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: 1000\r\n\r\ntext="); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("a stalled body on a refused path held the connection past the body deadline: %v", err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("a refused path = %d", res.StatusCode)
	}
}

type proving struct{ http.Handler }

func (proving) PhoneSession(*http.Request) bool { return true }

func TestAPairedRequestMayTakeLongerThanTheBodyDeadlineToSendItsBody(t *testing.T) {
	old := peerBodyWait
	peerBodyWait = 150 * time.Millisecond
	t.Cleanup(func() { peerBodyWait = old })
	read := make(chan error, 1)
	h := NewHost(HostOptions{
		Loopback: true,
		Resolve:  func(string) (string, error) { return "/home", nil },
		Open: func(id, _ string) (http.Handler, io.Closer, error) {
			return proving{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				read <- r.ParseForm()
				_, _ = io.WriteString(w, "ok")
			})}, io.NopCloser(nil), nil
		},
	})
	conn, err := net.Dial("tcp", servePeer(t, h))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, "POST /f0123456789ab/supervisor/send HTTP/1.1\r\nHost: x\r\nCookie: hand_phone_f0123456789ab=1\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: 9\r\n\r\ntext="); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := io.WriteString(conn, "abcd"); err != nil {
		t.Fatal(err)
	}
	if err := <-read; err != nil {
		t.Fatalf("a paired phone's slow body was cut: %v", err)
	}
}

func TestAPeerRequestProvesNoFleetByALaptopCookie(t *testing.T) {
	h := NewHost(HostOptions{Loopback: true, Valid: func(string, string) bool { return true }, Resolve: func(string) (string, error) { return "", errors.New("none") }})
	const current, other = "f0000000000aa", "f0123456789ab"
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName + "_" + other, Value: "x"})
	if !h.tokenProves(current, other, req) || len(h.tokenFleets(current, req)) != 2 {
		t.Fatal("the main listener does not read the laptop cookie")
	}
	req = req.WithContext(context.WithValue(req.Context(), peerKey{}, &peerConn{}))
	if h.tokenProves(current, other, req) || len(h.tokenFleets(current, req)) != 1 {
		t.Fatal("a peer connection proved a fleet with a laptop cookie")
	}
}

func servePeerProbe(t *testing.T, read func(r *http.Request)) string {
	t.Helper()
	p := NewPeer(probeHost(read), "secret")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = p.Serve(ln) }()
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	return ln.Addr().String()
}

func TestARequestHeldPastItsPeersHalfCloseIsAnsweredNormally(t *testing.T) {
	var canceled atomic.Bool
	addr := servePeerProbe(t, func(r *http.Request) {
		select {
		case <-r.Context().Done():
			canceled.Store(true)
		case <-time.After(500 * time.Millisecond):
		}
	})
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "GET /f0123456789ab/pair HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if b, _ := io.ReadAll(res.Body); string(b) != "ok" || canceled.Load() {
		t.Fatalf("body %q, canceled %v", b, canceled.Load())
	}
}

func TestARequestIsCanceledAndLeaksNothingWhenItsPeerResetsTheConnection(t *testing.T) {
	before := runtime.NumGoroutine()
	canceled := make(chan struct{})
	addr := servePeerProbe(t, func(r *http.Request) {
		<-r.Context().Done()
		close(canceled)
	})
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "GET /f0123456789ab/pair HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	_ = conn.(*net.TCPConn).SetLinger(0)
	_ = conn.Close()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("a reset connection left its request running")
	}
	for end := time.Now().Add(3 * time.Second); runtime.NumGoroutine() > before+3 && time.Now().Before(end); {
		time.Sleep(20 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before+3 {
		t.Fatalf("goroutines %d, before %d", n, before)
	}
}
