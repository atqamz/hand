package proc

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	if path := os.Getenv("HAND_PROC_MARK"); path != "" {
		if err := os.WriteFile(path, []byte("ran"), 0o600); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestDetachPathWithSpace(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "with space")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	copy := filepath.Join(dir, filepath.Base(exe))
	src, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.OpenFile(copy, os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
	mark := filepath.Join(dir, "mark")
	cmd := exec.Command(copy)
	cmd.Env = append(os.Environ(), "HAND_PROC_MARK="+mark)
	Detach(cmd)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mark); err != nil {
		t.Fatal(err)
	}
}

func TestNewGroupRuns(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mark := filepath.Join(t.TempDir(), "mark")
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), "HAND_PROC_MARK="+mark)
	NewGroup(cmd)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mark); err != nil {
		t.Fatal(err)
	}
}
