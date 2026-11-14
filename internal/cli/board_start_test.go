package cli_test

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fakebin"
)

func proofMain([]string) int {
	p := fakebin.Params()
	token := ""
	if b, err := os.ReadFile(filepath.Join(p["home"], "board.token")); err == nil {
		token = strings.TrimSpace(string(b))
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.WriteFile(filepath.Join(p["root"], "board.addr"), []byte(ln.Addr().String()+"\n"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fakebin.Append(p["started"], "started")
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/proof") {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, board.Proof(token, r.URL.Query().Get("nonce")))
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go srv.Serve(ln)
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(p["stop"]); err == nil {
			break
		}
	}
	_ = srv.Close()
	_ = os.Remove(filepath.Join(p["root"], "board.addr"))
	fakebin.Append(p["done"], "done")
	return 0
}

type boardStartFixture struct {
	*openFixture
	bin     string
	started string
	stop    string
	done    string
}

func newBoardStartFixture(t *testing.T) *boardStartFixture {
	t.Helper()
	fx := openWithoutBoard(t)
	dir := t.TempDir()
	bf := &boardStartFixture{
		openFixture: fx,
		started:     filepath.Join(dir, "started"),
		stop:        filepath.Join(dir, "stop"),
		done:        filepath.Join(dir, "done"),
	}
	proof := fakebin.Install(t, dir, "hand-proof", "proof", map[string]string{
		"root":    fx.h.vars["SECONDHAND_HOME"],
		"home":    fx.h.home,
		"started": bf.started,
		"stop":    bf.stop,
		"done":    bf.done,
	})
	bf.bin = proof
	*cli.WatchExe = func() (string, error) { return proof, nil }
	t.Cleanup(func() {
		_ = os.WriteFile(bf.stop, nil, 0o600)
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if _, err := os.Stat(bf.done); err == nil {
				return
			}
		}
		t.Errorf("the fake board did not stop")
	})
	return bf
}

func starts(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "started")
}

func TestBoardStartStartsADetachedBoardWithoutPrintingTheToken(t *testing.T) {
	fx := newBoardStartFixture(t)
	out, errOut, code := fx.h.run("board", "start")
	if code != 0 {
		t.Fatalf("board start: exit %d: %s", code, errOut)
	}
	if field(out, "board") != "running" {
		t.Fatalf("board start = %q", out)
	}
	if token := fx.token(t); strings.Contains(out+errOut, token) {
		t.Fatalf("the verb printed the token: %q %q", out, errOut)
	}
	if n := starts(t, fx.started); n != 1 {
		t.Fatalf("boards started = %d, want 1", n)
	}
	root := fx.h.vars["SECONDHAND_HOME"]
	if _, err := os.Stat(filepath.Join(root, "board.start.lock")); err != nil {
		t.Fatalf("no board.start.lock: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "board.log")); err != nil {
		t.Fatalf("no board.log: %v", err)
	}
}

func TestBoardStartTakesTheLockSoOneBoardStarts(t *testing.T) {
	fx := newBoardStartFixture(t)
	var wg sync.WaitGroup
	codes := make(chan int, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, code := fx.h.run("board", "start")
			codes <- code
		}()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 0 {
			t.Fatalf("a concurrent start failed with %d", code)
		}
	}
	if n := starts(t, fx.started); n != 1 {
		t.Fatalf("boards started = %d, want 1", n)
	}
}

func TestBoardStartWaitsTenSecondsByDefault(t *testing.T) {
	if *cli.BoardWait != 10*time.Second {
		t.Fatalf("the bound is %s, want 10s", *cli.BoardWait)
	}
	fx := openWithoutBoard(t)
	stop := filepath.Join(t.TempDir(), "stop")
	slow := fakebin.Install(t, t.TempDir(), "slow", "linger", map[string]string{"stop": stop})
	*cli.WatchExe = func() (string, error) { return slow, nil }
	wait := *cli.BoardWait
	*cli.BoardWait = 300 * time.Millisecond
	t.Cleanup(func() {
		*cli.BoardWait = wait
		_ = os.WriteFile(stop, nil, 0o600)
	})
	_, errOut, code := fx.h.run("board", "start")
	if code != 3 || !strings.Contains(errOut, "the board did not answer within 300ms") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestBoardStartSurfacesTheBoardsLastLine(t *testing.T) {
	fx := openWithoutBoard(t)
	dead := fakebin.Install(t, t.TempDir(), "dead", "die", nil)
	*cli.WatchExe = func() (string, error) { return dead, nil }
	_, errOut, code := fx.h.run("board", "start")
	if code != 3 {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	lines := strings.Split(strings.TrimSpace(errOut), "\n")
	if last := lines[len(lines)-1]; !strings.Contains(last, "board cannot start") {
		t.Fatalf("last line = %q of %q", last, errOut)
	}
}

func TestBoardStartUsesTheRecordedPath(t *testing.T) {
	fx := openWithoutBoard(t)
	root := fx.h.vars["SECONDHAND_HOME"]
	if err := os.WriteFile(filepath.Join(root, "board.path"), []byte("/opt/recorded/bin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "env")
	env := fakebin.Install(t, t.TempDir(), "env", "env", map[string]string{"out": out})
	*cli.WatchExe = func() (string, error) { return env, nil }
	_, _, _ = fx.h.run("board", "start")
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "PATH=/opt/recorded/bin") {
		t.Fatalf("the board's env = %q", b)
	}
}

func TestOpeningFromATerminalRecordsThePath(t *testing.T) {
	fx := newBoardStartFixture(t)
	fx.h.tty = true
	fx.h.ok("open", "--print")
	root := fx.h.vars["SECONDHAND_HOME"]
	b, err := os.ReadFile(filepath.Join(root, "board.path"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(b)), fx.h.vars["PATH"]; got != want {
		t.Fatalf("board.path = %q, want %q", got, want)
	}
}
