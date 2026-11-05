package board

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
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
