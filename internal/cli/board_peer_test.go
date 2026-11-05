package cli_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

var peerID = strings.Repeat("c", 64)

func freeLoopback(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func peerFile(h *harness) string { return filepath.Join(h.vars["SECONDHAND_HOME"], "board.peer") }

func peerGet(t *testing.T, addr, preface, path string) (int, error) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	req, err := http.NewRequest("GET", "http://localhost"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, preface); err != nil {
		t.Fatal(err)
	}
	if err := req.Write(conn); err != nil {
		return 0, err
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return 0, err
	}
	_ = res.Body.Close()
	return res.StatusCode, nil
}

func TestBoardOpensNoPeerListenerAndNoSecretByDefault(t *testing.T) {
	h := initWithProject(t)
	_, stop := startBoard(t, h, "127.0.0.1")
	out := stop()
	if strings.Contains(out, "peer:") {
		t.Fatalf("board prints a peer address by default: %q", out)
	}
	if _, err := os.Stat(peerFile(h)); !os.IsNotExist(err) {
		t.Fatalf("board.peer exists without --peer-addr: %v", err)
	}
}

func TestBoardPeerAddrServesTheSameBoardAsRemoteWithAPrivateSecret(t *testing.T) {
	h := initWithProject(t)
	peer := freeLoopback(t)
	base, stop := startBoard(t, h, "127.0.0.1", "--peer-addr", peer)
	fleetURL, token := fleetPage(t, base, h)
	id := fleetURL[strings.LastIndex(fleetURL, "/")+1:]
	b, err := os.ReadFile(peerFile(h))
	secret := strings.TrimSpace(string(b))
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{48}$`).MatchString(secret) || secret == token {
		t.Fatalf("board.peer = %q, %v", b, err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(peerFile(h)); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("board.peer mode = %v, %v", info, err)
		}
	}
	good := "HAND-PEER 1 " + peerID + " " + secret + "\n"
	for name, preface := range map[string]string{"no preface": "", "the right preface": good} {
		if code, err := peerGet(t, peer, preface, "/"+id+"/pair"); err != nil || code != http.StatusOK {
			t.Fatalf("%s: the pair page = %d, %v", name, code, err)
		}
		if code, err := peerGet(t, peer, preface, "/"+id+"/?token="+token); err != nil || code != http.StatusForbidden {
			t.Fatalf("%s: a token link over the peer listener = %d, %v", name, code, err)
		}
	}
	bad := "HAND-PEER 1 " + peerID + " " + strings.Repeat("0", 48) + "\n"
	if code, err := peerGet(t, peer, bad, "/"+id+"/pair"); err == nil {
		t.Fatalf("a wrong preface was answered %d", code)
	}
	if code, _ := login(t, fleetURL, token); code != http.StatusOK {
		t.Fatalf("the main listener's login = %d", code)
	}
	out := stop()
	if !strings.Contains(out, "peer: \""+peer+"\"") && !strings.Contains(out, "peer: "+peer) {
		t.Fatalf("the board does not print its peer address: %q", out)
	}
	if strings.Contains(out, secret) {
		t.Fatalf("the board printed the peer secret: %q", out)
	}
	if addr, _ := os.ReadFile(addrFile(h)); strings.Contains(string(addr), secret) {
		t.Fatal("board.addr holds the peer secret")
	}
}

func TestBoardKeepsItsPeerSecretAcrossRestartsAndRefusesAMalformedOne(t *testing.T) {
	h := initWithProject(t)
	_, stop := startBoard(t, h, "127.0.0.1", "--peer-addr", freeLoopback(t))
	first, err := os.ReadFile(peerFile(h))
	if err != nil {
		t.Fatal(err)
	}
	stop()
	_, stop = startBoard(t, h, "127.0.0.1", "--peer-addr", freeLoopback(t))
	again, _ := os.ReadFile(peerFile(h))
	stop()
	if string(first) != string(again) {
		t.Fatal("the peer secret changed across a restart")
	}
	if err := os.WriteFile(peerFile(h), []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := h.run("board", "--addr", "127.0.0.1:0", "--peer-addr", "127.0.0.1:0")
	if code == 0 || !strings.Contains(errOut, "not a peer secret") || strings.Contains(errOut, "short") {
		t.Fatalf("a malformed board.peer: exit %d, %q", code, errOut)
	}
}

func TestBoardPeerAddrMustBeLoopbackOnALoopbackBoard(t *testing.T) {
	h := initWithProject(t)
	for _, args := range [][]string{
		{"--addr", "127.0.0.1:0", "--peer-addr", "0.0.0.0:0"},
		{"--addr", "127.0.0.1:0", "--peer-addr", "not-an-address"},
		{"--addr", "0.0.0.0:0", "--peer-addr", "127.0.0.1:0"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, errOut, code := h.runCtx(ctx, append([]string{"board"}, args...)...)
		cancel()
		if code == 0 || !strings.Contains(errOut, "peer-addr") && !strings.Contains(errOut, "address") {
			t.Fatalf("board %v: exit %d, %q", args, code, errOut)
		}
		if _, err := os.Stat(peerFile(h)); !os.IsNotExist(err) {
			t.Fatalf("board %v wrote a secret before it refused: %v", args, err)
		}
	}
}
