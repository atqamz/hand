package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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
	if b, err := os.ReadFile(path); err == nil {
		if t := string(bytes.TrimSpace(b)); len(t) == 48 {
			return t, nil
		}
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return token, os.Chmod(path, 0o600)
}
