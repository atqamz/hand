package flock

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if path := os.Getenv("HAND_FLOCK_HOLD"); path != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			os.Exit(1)
		}
		if ok, err := Lock(f, false); !ok || err != nil {
			os.Exit(1)
		}
		_, _ = os.Stdout.WriteString("held\n")
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func open(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestLockExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	first, second := open(t, path), open(t, path)
	if ok, err := Lock(first, false); !ok || err != nil {
		t.Fatalf("first lock = %v, %v", ok, err)
	}
	if ok, err := Lock(second, false); ok || err != nil {
		t.Fatalf("second lock = %v, %v; want false, nil", ok, err)
	}
	_ = first.Close()
	if ok, err := Lock(second, false); !ok || err != nil {
		t.Fatalf("lock after release = %v, %v", ok, err)
	}
}

func TestLockReleasedOnExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), "HAND_FLOCK_HOLD="+path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(out, make([]byte, 5)); err != nil {
		t.Fatal("child did not take the lock")
	}
	f := open(t, path)
	if ok, err := Lock(f, false); ok || err != nil {
		t.Fatalf("lock while held = %v, %v; want false, nil", ok, err)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if ok, err := Lock(f, true); !ok || err != nil {
		t.Fatalf("lock after kill = %v, %v", ok, err)
	}
}
