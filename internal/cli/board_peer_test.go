package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
)

var peerID = strings.Repeat("c", 64)

type liveBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *liveBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *liveBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func startPeerBoard(t *testing.T, h *harness) (string, string, func() string) {
	t.Helper()
	_ = os.Remove(addrFile(h))
	ctx, cancel := context.WithCancel(context.Background())
	var out liveBuffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		env := h.env(&out, &out)
		env.Context = ctx
		cli.Run([]string{"board", "--addr", "127.0.0.1:0", "--peer-addr", "127.0.0.1:0"}, env)
	}()
	var addr, peer string
	eventually(t, func() bool {
		b, _ := os.ReadFile(addrFile(h))
		addr, peer = strings.TrimSpace(string(b)), field(out.String(), "peer")
		return addr != "" && peer != ""
	})
	stop := sync.OnceValue(func() string { cancel(); <-done; return out.String() })
	t.Cleanup(func() { stop() })
	return "http://" + addr, peer, stop
}

func boardRefusal(h *harness, args ...string) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, errOut, code := h.runCtx(ctx, append([]string{"board"}, args...)...)
	return errOut, code
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
	base, peer, stop := startPeerBoard(t, h)
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

func TestBoardKeepsItsPeerSecretAcrossRestarts(t *testing.T) {
	h := initWithProject(t)
	_, _, stop := startPeerBoard(t, h)
	first, err := os.ReadFile(peerFile(h))
	if err != nil {
		t.Fatal(err)
	}
	stop()
	_, _, stop = startPeerBoard(t, h)
	again, _ := os.ReadFile(peerFile(h))
	stop()
	if string(first) != string(again) {
		t.Fatal("the peer secret changed across a restart")
	}
}

func TestBoardRefusesAPeerSecretThatIsNotFortyEightHexDigits(t *testing.T) {
	h := initWithProject(t)
	for name, content := range map[string]string{
		"not hex":        "short\n",
		"hex but short":  "abcd\n",
		"hex but long":   strings.Repeat("a", 50) + "\n",
		"48 but not hex": strings.Repeat("g", 48) + "\n",
		"empty":          "",
	} {
		if err := os.WriteFile(peerFile(h), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		errOut, code := boardRefusal(h, "--addr", "127.0.0.1:0", "--peer-addr", "127.0.0.1:0")
		want := fmt.Sprintf("%s is not a peer secret; delete it to make a new one", peerFile(h))
		if code == 0 || !strings.Contains(errOut, want) {
			t.Fatalf("%s: exit %d, %q, want %q", name, code, errOut, want)
		}
		if shown := strings.ReplaceAll(errOut, peerFile(h), ""); strings.TrimSpace(content) != "" && strings.Contains(shown, strings.TrimSpace(content)) {
			t.Fatalf("%s: the error prints the file's contents: %q", name, errOut)
		}
	}
}

func TestBoardTightensALoosePeerSecretFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no file modes")
	}
	h := initWithProject(t)
	_, _, stop := startPeerBoard(t, h)
	secret, err := os.ReadFile(peerFile(h))
	if err != nil {
		t.Fatal(err)
	}
	stop()
	if err := os.Chmod(peerFile(h), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, stop = startPeerBoard(t, h)
	stop()
	info, err := os.Stat(peerFile(h))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("board.peer mode = %v, %v", info, err)
	}
	if again, _ := os.ReadFile(peerFile(h)); string(again) != string(secret) {
		t.Fatal("the secret changed while it was tightened")
	}
}

func TestBoardRefusesAPeerSecretThatIsNotARegularFile(t *testing.T) {
	h := initWithProject(t)
	elsewhere := filepath.Join(t.TempDir(), "other")
	if err := os.WriteFile(elsewhere, []byte(strings.Repeat("a", 48)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := func() error { return os.Symlink(elsewhere, peerFile(h)) }
	dir := func() error { return os.Mkdir(peerFile(h), 0o700) }
	for name, setup := range map[string]func() error{"a symlink to a secret": link, "a directory": dir} {
		if name == "a symlink to a secret" && runtime.GOOS == "windows" {
			continue
		}
		if err := setup(); err != nil {
			t.Fatal(err)
		}
		errOut, code := boardRefusal(h, "--addr", "127.0.0.1:0", "--peer-addr", "127.0.0.1:0")
		want := fmt.Sprintf("%s is not a regular file; delete it to make a new peer secret", peerFile(h))
		if code == 0 || !strings.Contains(errOut, want) {
			t.Fatalf("%s: exit %d, %q, want %q", name, code, errOut, want)
		}
		if err := os.Remove(peerFile(h)); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(elsewhere); err != nil || info.Mode().Perm() != 0o644 {
			t.Fatalf("the file behind the link was changed: %v, %v", info, err)
		}
	}
}

func TestBoardPeerAddrMustBeLoopbackOnALoopbackBoard(t *testing.T) {
	h := initWithProject(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	_, busyPort, _ := net.SplitHostPort(busy.Addr().String())
	loopback := "board: --peer-addr must be a loopback address"
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"any address":                {[]string{"--addr", "127.0.0.1:0", "--peer-addr", "0.0.0.0:0"}, loopback},
		"no host":                    {[]string{"--addr", "127.0.0.1:0", "--peer-addr", ":0"}, loopback},
		"any address on a busy port": {[]string{"--addr", "127.0.0.1:0", "--peer-addr", "0.0.0.0:" + busyPort}, loopback},
		"not an address":             {[]string{"--addr", "127.0.0.1:0", "--peer-addr", "not-an-address"}, "address not-an-address: missing port in address"},
		"a network board":            {[]string{"--addr", "0.0.0.0:0", "--peer-addr", "127.0.0.1:0"}, "board: --peer-addr needs --addr on a loopback address"},
	} {
		errOut, code := boardRefusal(h, c.args...)
		if code == 0 || !strings.Contains(errOut, c.want) {
			t.Fatalf("%s: exit %d, %q, want %q", name, code, errOut, c.want)
		}
		if _, err := os.Stat(peerFile(h)); !os.IsNotExist(err) {
			t.Fatalf("%s wrote a secret before it refused: %v", name, err)
		}
	}
}
