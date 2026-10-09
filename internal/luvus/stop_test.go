package luvus_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
)

func TestVersionReadsPing(t *testing.T) {
	srv := fakeuhp.Start(t, sock(t))
	version := "0.14.3"
	srv.Handle("ping", func(json.RawMessage) (any, error) { return map[string]any{"type": "pong", "version": version}, nil })
	c := luvus.Client{Socket: srv.Socket}
	if got, err := c.Version(context.Background()); err != nil || got != "0.14.3" {
		t.Fatalf("version = %q, %v", got, err)
	}
	version = ""
	if got, err := c.Version(context.Background()); err != nil || got != "" {
		t.Fatalf("no version = %q, %v", got, err)
	}
	srv.Handle("ping", func(json.RawMessage) (any, error) { return nil, fakeuhp.Fail{Code: "busy", Message: "starting"} })
	if _, err := c.Version(context.Background()); err == nil || errors.Is(err, luvus.ErrUnreachable) {
		t.Fatalf("a refusing server err = %v", err)
	}
	if _, err := (luvus.Client{Socket: sock(t)}).Version(context.Background()); !errors.Is(err, luvus.ErrUnreachable) {
		t.Fatalf("absent server err = %v", err)
	}
}

type fakeServer struct {
	uhp    *fakeuhp.Server
	dir    string
	cmd    *exec.Cmd
	marker string
	forced atomic.Int32
	once   sync.Once
}

func (s *fakeServer) kill() {
	s.once.Do(func() {
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
	})
}

func (s *fakeServer) forceKill(int, string) error {
	s.forced.Add(1)
	s.kill()
	return nil
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	sleep := fakebin.Install(t, t.TempDir(), "sleep", "sleep", nil)
	cmd := exec.Command(sleep)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{uhp: fakeuhp.Start(t, sock(t)), dir: t.TempDir(), cmd: cmd}
	t.Cleanup(s.kill)
	var err error
	if s.marker, err = luvus.ProcStartMarker(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	s.writePID(t, s.marker)
	s.uhp.Handle("ping", func(json.RawMessage) (any, error) { return map[string]any{"version": "0.14.3"}, nil })
	*luvus.ServerExitBy = 400 * time.Millisecond
	t.Cleanup(func(d time.Duration) func() { return func() { *luvus.ServerExitBy = d } }(*luvus.ServerExitBy))
	return s
}

func (s *fakeServer) writePID(t *testing.T, marker string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(s.dir, "server.pid"), []byte(fmt.Sprintf("%d %s\n", s.cmd.Process.Pid, marker)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (s *fakeServer) client() luvus.Client {
	return luvus.Client{Socket: s.uhp.Socket, Timeout: 300 * time.Millisecond}
}

func (s *fakeServer) alive() bool {
	m, err := luvus.ProcStartMarker(s.cmd.Process.Pid)
	return err == nil && m == s.marker
}

func TestStopServerWaitsForThePIDAndItsMarker(t *testing.T) {
	s := newFakeServer(t)
	*luvus.ServerExitBy = 5 * time.Second
	s.uhp.Handle("server.stop", func(json.RawMessage) (any, error) {
		go func() {
			time.Sleep(300 * time.Millisecond)
			s.kill()
			time.Sleep(300 * time.Millisecond)
			_ = os.Remove(filepath.Join(s.dir, "server.pid"))
		}()
		return map[string]any{"type": "server_stopping"}, nil
	})
	began := time.Now()
	if err := s.client().StopServer(context.Background(), s.dir, s.forceKill); err != nil {
		t.Fatal(err)
	}
	if time.Since(began) < 550*time.Millisecond || s.forced.Load() != 0 || s.alive() {
		t.Fatalf("stopped after %s, forced %d, alive %v", time.Since(began), s.forced.Load(), s.alive())
	}
	if _, err := os.Stat(filepath.Join(s.dir, "server.pid")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("server.pid err = %v", err)
	}
}

func TestStopServerWaitsForTheFileToo(t *testing.T) {
	s := newFakeServer(t)
	s.uhp.Handle("server.stop", func(json.RawMessage) (any, error) {
		go s.kill()
		return map[string]any{}, nil
	})
	if err := s.client().StopServer(context.Background(), s.dir, func(int, string) error { s.forced.Add(1); return nil }); err != nil || s.forced.Load() != 1 {
		t.Fatalf("err = %v, forced %d", err, s.forced.Load())
	}
}

func TestStopServerForcesOnlyAnAcknowledgedOrMuteServer(t *testing.T) {
	refuse := func(json.RawMessage) (any, error) { return nil, fakeuhp.Fail{Code: "busy", Message: "no"} }
	drop := func(json.RawMessage) (any, error) { return nil, fakeuhp.Drop }
	ack := func(json.RawMessage) (any, error) { return map[string]any{}, nil }
	for name, c := range map[string]struct {
		stop, ping func(json.RawMessage) (any, error)
		forced     int32
	}{
		"acknowledged and answering": {stop: ack, forced: 1},
		"mute":                       {stop: drop, ping: drop, forced: 1},
		"refusing but answering":     {stop: refuse, forced: 0},
	} {
		t.Run(name, func(t *testing.T) {
			s := newFakeServer(t)
			s.uhp.Handle("server.stop", c.stop)
			if c.ping != nil {
				s.uhp.Handle("ping", c.ping)
			}
			err := s.client().StopServer(context.Background(), s.dir, s.forceKill)
			if s.forced.Load() != c.forced || (err != nil) != (c.forced == 0) || s.alive() == (c.forced == 1) {
				t.Fatalf("forced %d, err = %v, alive %v", s.forced.Load(), err, s.alive())
			}
		})
	}
}

func TestStopServerFailsWhenTheProcessSurvivesTheKill(t *testing.T) {
	s := newFakeServer(t)
	s.uhp.Handle("server.stop", func(json.RawMessage) (any, error) { return map[string]any{}, nil })
	if err := s.client().StopServer(context.Background(), s.dir, func(int, string) error { return nil }); err == nil || !s.alive() {
		t.Fatalf("a kill that left the server alive: err = %v, alive %v", err, s.alive())
	}
}

func TestStopServerWaitsAtMostItsBoundWhenPingHangs(t *testing.T) {
	s := newFakeServer(t)
	s.uhp.Handle("server.stop", func(json.RawMessage) (any, error) { go s.kill(); return map[string]any{}, nil })
	s.uhp.Handle("ping", func(json.RawMessage) (any, error) { time.Sleep(3 * time.Second); return map[string]any{}, nil })
	old := *luvus.PingWait
	*luvus.PingWait = 2 * time.Second
	t.Cleanup(func() { *luvus.PingWait = old })
	began := time.Now()
	if err := (luvus.Client{Socket: s.uhp.Socket}).StopServer(context.Background(), s.dir, s.forceKill); time.Since(began) > 1500*time.Millisecond {
		t.Fatalf("err = %v after %s, past the 400ms bound", err, time.Since(began))
	}
}

func TestStopServerIgnoresARecycledPID(t *testing.T) {
	s := newFakeServer(t)
	s.writePID(t, "another marker")
	s.uhp.Handle("server.stop", func(json.RawMessage) (any, error) { return map[string]any{}, nil })
	gone := luvus.Client{Socket: sock(t)}
	if err := gone.StopServer(context.Background(), s.dir, s.forceKill); err != nil || s.forced.Load() != 0 || !s.alive() {
		t.Fatalf("err = %v, forced %d, alive %v", err, s.forced.Load(), s.alive())
	}
	if err := s.client().StopServer(context.Background(), s.dir, s.forceKill); err == nil || s.forced.Load() != 0 || !s.alive() || len(s.uhp.Calls("server.stop")) != 0 {
		t.Fatalf("an answering server with a recycled pid: err = %v, forced %d, alive %v", err, s.forced.Load(), s.alive())
	}
}

func TestStopServerDoesNotTrustAnUnusablePIDFile(t *testing.T) {
	for name, content := range map[string]string{"empty": "", "garbage": "not a pid\n", "no marker": "1\n", "zero pid": "0 marker\n", "negative pid": "-1 marker\n"} {
		t.Run(name, func(t *testing.T) {
			s := newFakeServer(t)
			if err := os.WriteFile(filepath.Join(s.dir, "server.pid"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			s.uhp.Handle("server.stop", func(json.RawMessage) (any, error) { return map[string]any{}, nil })
			if err := s.client().StopServer(context.Background(), s.dir, s.forceKill); err == nil || s.forced.Load() != 0 || len(s.uhp.Calls("server.stop")) != 0 {
				t.Fatalf("an answering server: err = %v, forced %d", err, s.forced.Load())
			}
			if err := (luvus.Client{Socket: sock(t)}).StopServer(context.Background(), s.dir, s.forceKill); err != nil || s.forced.Load() != 0 {
				t.Fatalf("a server that is gone: err = %v, forced %d", err, s.forced.Load())
			}
		})
	}
}

func TestStopServerReturnsAtOnceForALeftoverPIDFile(t *testing.T) {
	s := newFakeServer(t)
	*luvus.ServerExitBy = 5 * time.Second
	s.kill()
	gone := luvus.Client{Socket: sock(t)}
	began := time.Now()
	if err := gone.StopServer(context.Background(), s.dir, s.forceKill); err != nil || time.Since(began) > time.Second || s.forced.Load() != 0 {
		t.Fatalf("a crash left server.pid: err = %v after %s, forced %d", err, time.Since(began), s.forced.Load())
	}
	s.uhp.Handle("server.stop", func(json.RawMessage) (any, error) { return map[string]any{}, nil })
	s.uhp.Handle("ping", func(json.RawMessage) (any, error) { return nil, fakeuhp.Drop })
	began = time.Now()
	if err := s.client().StopServer(context.Background(), s.dir, s.forceKill); err != nil || time.Since(began) > time.Second || s.forced.Load() != 0 {
		t.Fatalf("the process was gone and ping fails: err = %v after %s, forced %d", err, time.Since(began), s.forced.Load())
	}
}

func TestPingIsBounded(t *testing.T) {
	s := newFakeServer(t)
	s.uhp.Handle("ping", func(json.RawMessage) (any, error) { time.Sleep(3 * time.Second); return map[string]any{}, nil })
	old := *luvus.PingWait
	*luvus.PingWait = 200 * time.Millisecond
	t.Cleanup(func() { *luvus.PingWait = old })
	c := luvus.Client{Socket: s.uhp.Socket}
	began := time.Now()
	if _, err := c.Version(context.Background()); err == nil || time.Since(began) > 2*time.Second {
		t.Fatalf("version err = %v after %s", err, time.Since(began))
	}
	s.kill()
	began = time.Now()
	if err := c.StopServer(context.Background(), s.dir, s.forceKill); err != nil || time.Since(began) > 2*time.Second || s.forced.Load() != 0 {
		t.Fatalf("a mute server and a dead process: err = %v after %s, forced %d", err, time.Since(began), s.forced.Load())
	}
}
