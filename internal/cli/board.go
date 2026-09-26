package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
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
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	token, err := boardToken(r.home)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("%w: %v", state.ErrInvalid, err)
	}
	ctx, stop := signal.NotifyContext(r.ctx(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{Handler: board.New(st, token), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	var d toon.Doc
	d.Field("board", "http://"+ln.Addr().String()+"/?token="+token)
	if host, _, err := net.SplitHostPort(ln.Addr().String()); err == nil {
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			d.Field("warning", "plain HTTP on a network address: anyone who can see this traffic can take the token; prefer a loopback board behind ssh -L or tailscale serve")
		}
	}
	d.Help("Keep the link private: the token is the only thing guarding the two board actions",
		"From a phone on the same network, run it with `--addr 0.0.0.0:7777` and open the machine's LAN address")
	if err := r.print(&d); err != nil {
		return err
	}
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func boardToken(home string) (string, error) {
	path := filepath.Join(home, "board.token")
	if token, err := readBoardToken(path); !errors.Is(err, fs.ErrNotExist) {
		return token, err
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	tmp, err := os.CreateTemp(home, ".board.token-")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.WriteString(token + "\n")
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return "", werr
	}
	if err := os.Link(tmp.Name(), path); errors.Is(err, fs.ErrExist) {
		return readBoardToken(path)
	} else if err != nil {
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
	if len(token) != 48 {
		return "", fmt.Errorf("%w: %s is not a board token; delete it to make a new one", state.ErrInvalid, path)
	}
	return token, nil
}
