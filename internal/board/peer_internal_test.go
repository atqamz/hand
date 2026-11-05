package board

import (
	"context"
	"errors"
	"io"
	"net"
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
