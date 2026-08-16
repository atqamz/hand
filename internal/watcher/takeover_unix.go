//go:build !windows

package watcher

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const sockaddrUnLimit = 104
const takeoverSocketDir = "/tmp"

// Derives the generation-bound Unix socket location from the fleet-home
// identity and ownership generation, not watch.pid. A short hashed name stays
// under macOS's sockaddr_un path limit, trusting no arbitrary path from metadata.
func takeoverSocketPath(home, gen string) string {
	sum := sha256.Sum256([]byte(canonicalHome(home) + "\x00" + gen))
	name := fmt.Sprintf("hw-%s.sock", hex.EncodeToString(sum[:])[:24])
	return filepath.Join(takeoverSocketDir, name)
}

// Owns the generation-bound Unix socket that lets a takeover contender ask the
// incumbent to step aside. The path derives from home identity + generation, so
// a stale or reused pid can never be the target of a takeover.
type takeoverEndpoint struct {
	listener   net.Listener
	requested  chan struct{}
	signalOnce sync.Once
	closeOnce  sync.Once
	handlers   sync.WaitGroup
	done       chan struct{}
	sockPath   string
}

func newTakeoverEndpoint(home, gen string) (*takeoverEndpoint, error) {
	sockPath := takeoverSocketPath(home, gen)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("listen on takeover socket: %w", err)
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(sockPath)
		return nil, fmt.Errorf("set takeover socket permissions: %w", err)
	}

	e := &takeoverEndpoint{
		listener:  ln,
		requested: make(chan struct{}),
		done:      make(chan struct{}),
		sockPath:  sockPath,
	}
	go e.accept(gen)
	return e, nil
}

func (e *takeoverEndpoint) Requested() <-chan struct{} {
	if e == nil {
		return nil
	}
	return e.requested
}

func (e *takeoverEndpoint) Close() {
	if e == nil {
		return
	}
	e.closeOnce.Do(func() {
		_ = e.listener.Close()
		<-e.done
		e.handlers.Wait()
		_ = os.Remove(e.sockPath)
	})
}

func (e *takeoverEndpoint) accept(gen string) {
	defer close(e.done)
	for {
		conn, err := e.listener.Accept()
		if err != nil {
			// Listener closed: normal teardown, never a takeover signal.
			return
		}
		e.handlers.Add(1)
		go e.handle(conn, gen)
	}
}

func (e *takeoverEndpoint) handle(conn net.Conn, gen string) {
	defer e.handlers.Done()
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil || strings.TrimSuffix(string(line), "\n") != gen {
		// Wrong generation or a malformed request must not trigger replacement.
		return
	}
	e.signalOnce.Do(func() { close(e.requested) })
	_, _ = conn.Write([]byte("ok\n"))
}

// Asks the incumbent that published gen to step aside by reaching its
// generation-bound socket. Dialing that socket is the proof: a stale or absent
// record can never target an unrelated process, nor fall back to pid.
func requestTakeover(home, gen string) error {
	conn, err := net.DialTimeout("unix", takeoverSocketPath(home, gen), time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := fmt.Fprintf(conn, "%s\n", gen); err != nil {
		return err
	}
	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return fmt.Errorf("read takeover acknowledgment: %w", err)
	}
	if reply != "ok\n" {
		return fmt.Errorf("unexpected takeover acknowledgment %q", reply)
	}
	return nil
}
