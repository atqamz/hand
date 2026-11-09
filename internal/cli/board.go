package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/flock"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/sidecar"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
	"github.com/atqamz/hand/internal/transcript"
)

func init() {
	commands["board"] = cmdBoard
}

var servePeer = func(p *board.Peer, ln net.Listener) error { return p.Serve(ln) }

func cmdBoard(r *runner, args []string) error {
	if len(args) > 0 && args[0] == "stop" {
		return cmdBoardStop(r, args[1:])
	}
	fs := flags("board")
	addr := fs.String("addr", "127.0.0.1:7777", "listen address; 0.0.0.0:7777 makes it reachable from a phone on the LAN")
	peerAddr := fs.String("peer-addr", "", "a second loopback address for a local tunnel or sidecar; every request on it counts as remote; off by default")
	iroh := fs.Bool("iroh", false, "start hand-iroh so a phone can reach the board over iroh; opens the peer listener on a free loopback port unless --peer-addr names one; off by default")
	every := fs.Duration("update-every", 0, "run `hand update` this often, which also switches a quiet fleet's Luvus server; 0 is off, the least is "+updateMin.String())
	if _, err := parse(fs, args, 0); err != nil {
		return err
	}
	if *every != 0 && *every < updateMin {
		return usageError{"board: --update-every must be 0 or at least " + updateMin.String()}
	}
	root, err := fleet.Root(r.env.Getenv)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("%w: %v", state.ErrInvalid, err)
	}
	ctx, stop := signal.NotifyContext(r.ctx(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	loopback := false
	local := ln.Addr().String()
	if host, port, err := net.SplitHostPort(local); err == nil {
		ip := net.ParseIP(host)
		loopback = ip != nil && ip.IsLoopback()
		if ip != nil && ip.IsUnspecified() {
			local = net.JoinHostPort("127.0.0.1", port)
		}
	}
	if *iroh && !loopback {
		_ = ln.Close()
		return usageError{"board: --iroh needs --addr on a loopback address"}
	}
	if *iroh && *peerAddr == "" {
		*peerAddr = "127.0.0.1:0"
	}
	pln, secret, err := listenPeer(root, *peerAddr, loopback)
	if err != nil {
		_ = ln.Close()
		return err
	}
	var side *sidecar.Sidecar
	var irohStatus func() sidecar.Status
	if pln != nil {
		defer pln.Close()
		if *iroh {
			side = r.newSidecar(root, pln.Addr().String())
			irohStatus = side.Status
		}
	}
	transcripts := &transcript.Reader{Paths: r.transcriptPaths()}
	var peer *board.Peer
	host := board.NewHost(board.HostOptions{
		Loopback: loopback,
		Resolve:  func(id string) (string, error) { return boardHome(root, id) },
		Open: func(id, home string) (http.Handler, io.Closer, error) {
			return r.openBoard(id, home, local, loopback, transcripts, irohStatus, func(endpoint string) {
				if peer != nil {
					peer.CloseEndpoint(endpoint)
				}
			})
		},
		List:  func() ([]board.FleetLink, error) { return boardFleets(root) },
		Valid: func(id, token string) bool { return boardTokenIs(root, id, token) },
	})
	defer host.Close()
	srv := &http.Server{Handler: host, ReadHeaderTimeout: 10 * time.Second}
	peer = board.NewPeer(host, secret)
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var drained sync.WaitGroup
		drained.Go(func() { _ = peer.Shutdown(sctx) })
		_ = srv.Shutdown(sctx)
		drained.Wait()
	}()
	forget, err := recordAddr(root, local)
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer forget()
	generation, forgetGeneration, err := recordGeneration(r.env, root)
	if err != nil {
		_ = ln.Close()
		return err
	}
	r.generation = generation
	defer forgetGeneration()
	defer r.writePID(filepath.Join(root, "board.pid"))()
	if *every > 0 && Channel == "source" {
		fmt.Fprintln(r.env.Stderr, "board: --update-every is off: this hand was built from source and has no update channel")
		*every = 0
	}
	if err := recordEvery(root, *every); err != nil {
		return err
	}
	if err := recordIroh(root, *iroh); err != nil {
		return err
	}
	var d toon.Doc
	d.Field("board", "http://"+local+"/")
	if pln != nil {
		d.Field("peer", pln.Addr().String())
	}
	if side != nil {
		d.Field("iroh", sidecar.Starting)
	}
	control := "Chat with the supervisor and control it from its fleet's page; reach it from a phone through ssh -L or tailscale serve"
	if !loopback {
		d.Field("warning", "plain HTTP on a network address: anyone who can see this traffic can take a fleet's token; prefer a loopback board behind ssh -L or tailscale serve")
		control = "Supervisor controls are off on a network address; answering decisions and acknowledging reports still work"
	}
	help := []string{"Open a fleet's page with `hand open` from inside the fleet; keep its link private, since the token is the only thing guarding that fleet", control}
	if side != nil {
		help = append(help, "`hand doctor` shows the iroh row and, once it is ready, the endpoint ID a phone dials")
	}
	d.Help(help...)
	if err := r.print(&d); err != nil {
		return err
	}
	keep, ran := make(chan struct{}), make(chan struct{})
	kctx, stopKeep := context.WithCancel(ctx)
	sctx, stopSide := context.WithCancel(ctx)
	go func() {
		defer close(keep)
		r.keepWatchers(kctx, root, *every)
	}()
	go func() {
		defer close(ran)
		if side != nil {
			side.Run(sctx)
		}
	}()
	var peerDone chan struct{}
	if pln != nil {
		peerDone = make(chan struct{})
		go func() {
			defer close(peerDone)
			if err := servePeer(peer, pln); !errors.Is(err, http.ErrServerClosed) {
				fmt.Fprintln(r.env.Stderr, "board: the peer listener stopped:", err)
				stopSide()
			}
		}()
	}
	err = srv.Serve(ln)
	stopKeep()
	stopSide()
	<-keep
	<-ran
	if peerDone != nil {
		_ = pln.Close()
		<-peerDone
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func listenPeer(root, addr string, loopback bool) (net.Listener, string, error) {
	if addr == "" {
		return nil, "", nil
	}
	if !loopback {
		return nil, "", usageError{"board: --peer-addr needs --addr on a loopback address"}
	}
	a, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", state.ErrInvalid, err)
	}
	if !a.IP.IsLoopback() {
		return nil, "", usageError{"board: --peer-addr must be a loopback address"}
	}
	ln, err := net.ListenTCP("tcp", a)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", state.ErrInvalid, err)
	}
	secret, err := fileSecret(filepath.Join(root, "board.peer"), "peer secret")
	if err != nil {
		_ = ln.Close()
		return nil, "", err
	}
	return ln, secret, nil
}

func boardHome(root, id string) (string, error) {
	home, err := fleet.Home(root, id)
	if errors.Is(err, state.ErrInvalid) {
		return "", fmt.Errorf("%w: %v", state.ErrNotFound, err)
	}
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(filepath.Join(home, "hand.db")); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: fleet %s has no hand.db at its registered home", state.ErrNotFound, id)
	}
	return home, nil
}

func (r *runner) openBoard(id, home, local string, loopback bool, transcripts *transcript.Reader, iroh func() sidecar.Status, closePeer func(string)) (http.Handler, io.Closer, error) {
	st, err := state.Open(filepath.Join(home, "hand.db"), r.env.Now)
	if err != nil {
		return nil, nil, err
	}
	f, err := st.Fleet(r.ctx())
	if err == nil && f.ID != id {
		err = fmt.Errorf("%w: %s holds fleet %s, not %s", state.ErrNotFound, home, f.ID, id)
	}
	henv := harness.EnvOf(r.env.Getenv)
	henv.Dir = home
	fb := &fleetBoard{home: home, st: st, opts: board.Options{
		Controls:   loopback,
		Control:    control(r.env, home, r.generation),
		Harness:    henv,
		Models:     harness.Options{Environ: r.env.Environ(), Dir: home, Now: r.env.Now},
		Luvus:      r.client(fleet.Session(id)),
		Transcript: transcripts,
		Home:       home,
		Base:       "/" + id,
		Phone:      func(ctx context.Context) board.PhoneLink { return r.phoneLink(ctx, local) },
		Iroh:       iroh,
		ClosePeer:  closePeer,
	}}
	if err == nil {
		_, err = fb.current()
	}
	if err != nil {
		_ = st.Close()
		return nil, nil, err
	}
	return fb, st, nil
}

type fleetBoard struct {
	home  string
	st    *state.Store
	opts  board.Options
	mu    sync.Mutex
	token os.FileInfo
	h     http.Handler
}

func (f *fleetBoard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h, err := f.current()
	if err != nil {
		http.Error(w, board.Scrub(err.Error()), http.StatusInternalServerError)
		return
	}
	h.ServeHTTP(w, r)
}

func (f *fleetBoard) PhoneSession(r *http.Request) bool {
	h, err := f.current()
	p, ok := h.(board.Prover)
	return err == nil && ok && p.PhoneSession(r)
}

func (f *fleetBoard) PairPhone(ctx context.Context, rd *board.Redeemed) (board.Paired, error) {
	h, err := f.current()
	if err != nil {
		return board.Paired{}, err
	}
	p, ok := h.(board.Pairer)
	if !ok {
		return board.Paired{}, state.ErrNotFound
	}
	return p.PairPhone(ctx, rd)
}

func (f *fleetBoard) PhoneRecords(ctx context.Context) ([]board.PhoneRecord, error) {
	h, err := f.current()
	if err != nil {
		return nil, err
	}
	l, ok := h.(board.Lister)
	if !ok {
		return nil, state.ErrNotFound
	}
	return l.PhoneRecords(ctx)
}

func (f *fleetBoard) RevokePhone(ctx context.Context, rv *board.Revocation) (int, error) {
	h, err := f.current()
	if err != nil {
		return 0, err
	}
	r, ok := h.(board.Revoker)
	if !ok {
		return 0, state.ErrNotFound
	}
	return r.RevokePhone(ctx, rv)
}

func (f *fleetBoard) current() (http.Handler, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := filepath.Join(f.home, "board.token")
	if info, err := os.Stat(path); err == nil && f.h != nil && os.SameFile(info, f.token) && info.ModTime().Equal(f.token.ModTime()) {
		return f.h, nil
	}
	for {
		before, _ := os.Stat(path)
		token, err := boardToken(f.home)
		if err != nil {
			return nil, err
		}
		after, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if before != nil && os.SameFile(before, after) && before.ModTime().Equal(after.ModTime()) {
			f.h, f.token = board.New(f.st, token, f.opts), after
			return f.h, nil
		}
	}
}

func boardFleets(root string) ([]board.FleetLink, error) {
	entries, err := fleet.List(root)
	if err != nil {
		return nil, err
	}
	var out []board.FleetLink
	for _, e := range entries {
		if e.State == "ok" {
			out = append(out, board.FleetLink{ID: e.ID, Name: e.Name})
		}
	}
	return out, nil
}

func boardTokenIs(root, id, token string) bool {
	home, err := boardHome(root, id)
	if err != nil {
		return false
	}
	want, err := readBoardToken(filepath.Join(home, "board.token"))
	return err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(token)) == 1
}

func recordAddr(root, addr string) (func(), error) {
	path := filepath.Join(root, "board.addr")
	unlock, err := lockPath(path + ".lock")
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := writeFile(root, path, addr+"\n"); err != nil {
		return nil, err
	}
	return func() {
		unlock, err := lockPath(path + ".lock")
		if err != nil {
			return
		}
		defer unlock()
		if b, err := os.ReadFile(path); err == nil && string(b) == addr+"\n" {
			_ = os.Remove(path)
		}
	}, nil
}

func lockPath(path string) (func(), error) {
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := flock.Lock(lock, true); err != nil {
		_ = lock.Close()
		return nil, err
	}
	return func() { _ = flock.Release(lock) }, nil
}

func writeFile(dir, path, body string) error {
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.WriteString(body)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	return os.Rename(tmp.Name(), path)
}

func (r *runner) transcriptPaths() transcript.Paths {
	getenv := r.env.Getenv
	claude := getenv("CLAUDE_CONFIG_DIR")
	if claude == "" {
		claude = filepath.Join(getenv("HOME"), ".claude")
	}
	data := getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(getenv("HOME"), ".local", "share")
	}
	opencode, _ := harness.LookPath("opencode", getenv("PATH"))
	return transcript.Paths{Claude: claude, Codex: harness.CodexHome(getenv), Opencode: opencode, OpencodeData: filepath.Join(data, "opencode"), Agy: harness.AgyConversations(getenv), Copilot: harness.CopilotHome(getenv), Omp: harness.OmpAgent(getenv, r.home)}
}

func boardToken(home string) (string, error) {
	return fileSecret(filepath.Join(home, "board.token"), "board token")
}

func fileSecret(path, what string) (string, error) {
	unlock, err := lockPath(path + ".lock")
	if err != nil {
		return "", err
	}
	defer unlock()
	if token, err := readSecret(path, what); !errors.Is(err, fs.ErrNotExist) {
		return token, err
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	if err := writeFile(filepath.Dir(path), path, token+"\n"); err != nil {
		return "", err
	}
	return token, nil
}

func readBoardToken(path string) (string, error) { return readSecret(path, "board token") }

var chmod = os.Chmod

func readSecret(path, what string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %s is not a regular file; delete it to make a new %s", state.ErrInvalid, path, what)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	_ = chmod(path, 0o600)
	token := string(bytes.TrimSpace(b))
	if _, err := hex.DecodeString(token); err != nil || len(token) != 48 {
		return "", fmt.Errorf("%w: %s is not a %s; delete it to make a new one", state.ErrInvalid, path, what)
	}
	return token, nil
}
