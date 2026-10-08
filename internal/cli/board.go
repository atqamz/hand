package cli

import (
	"bytes"
	"context"
	"crypto/rand"
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
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
	"github.com/atqamz/hand/internal/transcript"
)

func init() {
	commands["board"] = cmdBoard
}

func cmdBoard(r *runner, args []string) error {
	fs := flags("board")
	addr := fs.String("addr", "127.0.0.1:7777", "listen address; 0.0.0.0:7777 makes it reachable from a phone on the LAN")
	if _, err := parse(fs, args, 0); err != nil {
		return err
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
	transcripts := &transcript.Reader{Paths: r.transcriptPaths()}
	host := board.NewHost(board.HostOptions{
		Loopback: loopback,
		Resolve:  func(id string) (string, error) { return boardHome(root, id) },
		Open: func(id, home string) (http.Handler, io.Closer, error) {
			return r.openBoard(id, home, loopback, transcripts)
		},
		List: func() ([]board.FleetLink, error) { return boardFleets(root) },
	})
	defer host.Close()
	srv := &http.Server{Handler: host, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	forget, err := recordAddr(root, local)
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer forget()
	defer r.writePID(filepath.Join(root, "board.pid"))()
	var d toon.Doc
	d.Field("board", "http://"+local+"/")
	control := "Chat with the supervisor and control it from its fleet's page; reach it from a phone through ssh -L or tailscale serve"
	if !loopback {
		d.Field("warning", "plain HTTP on a network address: anyone who can see this traffic can take a fleet's token; prefer a loopback board behind ssh -L or tailscale serve")
		control = "Supervisor controls are off on a network address; answering decisions and acknowledging reports still work"
	}
	d.Help("Open a fleet's page with `hand open` from inside the fleet; keep its link private, since the token is the only thing guarding that fleet", control)
	if err := r.print(&d); err != nil {
		return err
	}
	keep := make(chan struct{})
	kctx, stopKeep := context.WithCancel(ctx)
	go func() {
		defer close(keep)
		r.keepWatchers(kctx, root)
	}()
	err = srv.Serve(ln)
	stopKeep()
	<-keep
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
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

func (r *runner) openBoard(id, home string, loopback bool, transcripts *transcript.Reader) (http.Handler, io.Closer, error) {
	st, err := state.Open(filepath.Join(home, "hand.db"), r.env.Now)
	if err != nil {
		return nil, nil, err
	}
	f, err := st.Fleet(r.ctx())
	if err == nil && f.ID != id {
		err = fmt.Errorf("%w: %s holds fleet %s, not %s", state.ErrNotFound, home, f.ID, id)
	}
	fb := &fleetBoard{home: home, st: st, opts: board.Options{
		Controls:   loopback,
		Control:    control(r.env, home),
		Harness:    harness.EnvOf(r.env.Getenv),
		Luvus:      r.client(fleet.Session(id)),
		Transcript: transcripts,
		Home:       home,
		Base:       "/" + id,
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
	return transcript.Paths{Claude: claude, Codex: harness.CodexHome(getenv), Opencode: opencode, OpencodeData: filepath.Join(data, "opencode"), Agy: harness.AgyConversations(getenv)}
}

func boardToken(home string) (string, error) {
	path := filepath.Join(home, "board.token")
	unlock, err := lockPath(path + ".lock")
	if err != nil {
		return "", err
	}
	defer unlock()
	if token, err := readBoardToken(path); !errors.Is(err, fs.ErrNotExist) {
		return token, err
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	if err := writeFile(home, path, token+"\n"); err != nil {
		return "", err
	}
	return token, nil
}

func readBoardToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}
	token := string(bytes.TrimSpace(b))
	if _, err := hex.DecodeString(token); err != nil || len(token) != 48 {
		return "", fmt.Errorf("%w: %s is not a board token; delete it to make a new one", state.ErrInvalid, path)
	}
	return token, nil
}
