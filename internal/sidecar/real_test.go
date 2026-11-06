package sidecar

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"time"
)

func realBinary(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("HAND_IROH_BIN")
	if bin == "" {
		t.Skip("HAND_IROH_BIN is not set: build the sidecar with `cargo build --locked` in iroh/ and point HAND_IROH_BIN at target/debug/hand-iroh (the Iroh workflow does)")
	}
	abs, err := filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestRealSidecarStartsKeepsItsIDAndStopsWhenItsInputCloses(t *testing.T) {
	bin := realBinary(t)
	if got, err := Verify(context.Background(), bin); err != nil || got != Version {
		t.Fatalf("Verify(%s) = %q, %v: the version this hand pins is not the one in iroh/Cargo.toml", bin, got, err)
	}
	dir := t.TempDir()
	secret := filepath.Join(dir, "board.peer")
	if err := os.WriteFile(secret, []byte("feedfacefeedfacefeedfacefeedfacefeedfacefeedface\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	keys := filepath.Join(dir, "iroh")
	idOf := func() string {
		rec := &recorder{}
		s := New(Options{
			Resolve:    func(context.Context) (string, error) { return bin, nil },
			Board:      "127.0.0.1:9",
			SecretFile: secret,
			KeyDir:     keys,
			Dir:        dir,
			Args:       []string{"--relay", "none"},
			Attempts:   1,
			StopWait:   30 * time.Second,
			OnChange:   rec.add,
		})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			s.Run(ctx)
		}()
		defer func() {
			cancel()
			<-done
		}()
		eventually(t, "the real sidecar to be ready", func() bool {
			st := s.Status()
			if st.State == Failed {
				t.Fatalf("the real sidecar failed: %+v", st)
			}
			return st.State == Ready
		})
		st := s.Status()
		if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(st.ID) || st.Relay != "none" {
			t.Fatalf("Status = %+v", st)
		}
		began := time.Now()
		cancel()
		select {
		case <-done:
		case <-time.After(25 * time.Second):
			t.Fatal("the real sidecar did not stop when its input closed")
		}
		if took := time.Since(began); took > 10*time.Second {
			t.Fatalf("the sidecar took %v to leave: it was killed, not closed", took)
		}
		if got := rec.states(); got[len(got)-1] != Stopped {
			t.Fatalf("states = %q", got)
		}
		return st.ID
	}
	first := idOf()
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(keys); err != nil || fi.Mode().Perm() != 0o700 {
			t.Fatalf("key folder = %v, %v; want mode 0700", fi, err)
		}
		if fi, err := os.Stat(filepath.Join(keys, "hand-iroh.key")); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("key file = %v, %v; want mode 0600", fi, err)
		}
	}
	if second := idOf(); second != first {
		t.Fatalf("the endpoint ID changed across runs: %s then %s", first, second)
	}
	if b, _ := os.ReadFile(filepath.Join(keys, "hand-iroh.key")); len(b) != 32 || string(b) == "feedfacefeedfacefeedfacefeedfacefeedfacefeedface\n" {
		t.Fatalf("key file holds %d bytes", len(b))
	}
}
