package board

import (
	"bufio"
	"context"
	"crypto/subtle"
	"io"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/atqamz/hand/internal/state"
)

const (
	prefaceWord  = "HAND-PEER "
	prefaceMax   = 256
	peerRequests = 32
	peerBytes    = 64 << 10
	peerHeaders  = 12 << 10
)

var (
	peerID       = regexp.MustCompile(`^[0-9a-z]{16,64}$`)
	silentClose  = io.EOF
	peerBodyWait = 10 * time.Second
	peerIdleWait = 30 * time.Second
)

type peerKey struct{}

type peerConn struct {
	net.Conn
	br       *bufio.Reader
	secret   string
	once     sync.Once
	err      error
	id       string
	read     atomic.Int64
	requests atomic.Int64
	paired   atomic.Bool
}

func peerOf(r *http.Request) *peerConn {
	c, _ := r.Context().Value(peerKey{}).(*peerConn)
	return c
}

func (c *peerConn) login() string {
	if c.id == "" {
		return ""
	}
	return "iroh:" + c.id
}

func (c *peerConn) addr() string {
	if c.id == "" {
		return "anonymous"
	}
	return c.id
}

func (c *peerConn) Read(p []byte) (int, error) {
	c.once.Do(c.open)
	if c.err != nil {
		return 0, c.err
	}
	n, err := c.br.Read(p)
	if c.read.Add(int64(n)) > peerBytes && !c.paired.Load() {
		_ = c.Conn.Close()
		return 0, silentClose
	}
	return n, err
}

func (c *peerConn) open() {
	for i := 1; i <= len(prefaceWord); i++ {
		b, err := c.br.Peek(i)
		if err != nil {
			c.err = err
			return
		}
		if b[i-1] != prefaceWord[i-1] {
			return
		}
	}
	line, err := c.br.ReadSlice('\n')
	f := strings.Split(strings.TrimSuffix(string(line), "\n"), " ")
	if err != nil || len(f) != 4 || f[1] != "1" || !peerID.MatchString(f[2]) || c.secret == "" || subtle.ConstantTimeCompare([]byte(f[3]), []byte(c.secret)) != 1 {
		_ = c.Conn.Close()
		c.err = silentClose
		return
	}
	c.id = f[2]
}

type peerListener struct {
	net.Listener
	secret string
}

func (l peerListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &peerConn{Conn: c, br: bufio.NewReaderSize(c, prefaceMax), secret: l.secret}, nil
}

type Peer struct {
	h      *Host
	srv    *http.Server
	secret string
}

func NewPeer(h *Host, secret string) *Peer {
	p := &Peer{h: h, secret: secret}
	p.srv = &http.Server{
		Handler:           p,
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    peerHeaders,
		IdleTimeout:       peerIdleWait,
		ConnContext:       func(ctx context.Context, c net.Conn) context.Context { return context.WithValue(ctx, peerKey{}, c) },
	}
	return p
}

func (p *Peer) Serve(ln net.Listener) error { return p.srv.Serve(peerListener{ln, p.secret}) }

func (p *Peer) Shutdown(ctx context.Context) error { return p.srv.Shutdown(ctx) }

func fleetRest(path string) string {
	id, rest, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if !state.FleetID.MatchString(id) {
		return ""
	}
	return rest
}

func unpairedPath(path string) bool {
	return strings.HasPrefix(path, "/static/") || fleetRest(path) == "pair"
}

func carriesPhoneCookie(r *http.Request) bool {
	return slices.ContainsFunc(r.Cookies(), func(c *http.Cookie) bool { return strings.HasPrefix(c.Name, phoneName+"_") })
}

func (p *Peer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	harden(w)
	c := peerOf(r)
	funnel := viaFunnel(r)
	for k := range r.Header {
		if k == "Forwarded" || strings.HasPrefix(k, "X-Forwarded-") || strings.HasPrefix(k, "Tailscale-") {
			delete(r.Header, k)
		}
	}
	proved := !funnel && len(p.h.phoneFleets(r, "")) > 0
	if proved {
		c.paired.Store(true)
	} else if carriesPhoneCookie(r) {
		c.paired.Store(false)
	}
	counted := !c.paired.Load()
	if counted {
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(peerBodyWait))
	}
	over := counted && c.requests.Add(1) > peerRequests
	public := unpairedPath(r.URL.Path)
	switch {
	case over:
		w.Header().Set("Connection", "close")
		hostFail(w, http.StatusTooManyRequests, "This connection has asked for too much before pairing. Pair this phone first, or open a new connection.")
	case funnel:
		hostFail(w, http.StatusForbidden, funnelRefusal)
	case !public && !proved:
		hostFail(w, http.StatusForbidden, sessionFaults(noSession, true))
	case fleetRest(r.URL.Path) == "proof":
		hostFail(w, http.StatusForbidden, "The proof check is not served on the peer listener.")
	default:
		p.h.ServeHTTP(w, r)
	}
}
