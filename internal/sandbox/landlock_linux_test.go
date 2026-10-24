//go:build linux

package sandbox

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestBoxedDeniesWhatLiesOutsideThePolicy(t *testing.T) {
	if p := Probe(); !p.UnixSockets {
		t.Skip(p.Reason)
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	wt := filepath.Join(root, "worktrees", "fleet", "t1-a2")
	decoy := filepath.Join(home, ".ssh", "id_ed25519")
	outside := filepath.Join(home, "outside.txt")
	sock := filepath.Join(root, "luvus.sock")
	if err := os.MkdirAll(wt, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(decoy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(decoy, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	abstract := fmt.Sprintf("@hand-sandbox-%d", os.Getpid())
	aln, err := net.Listen("unix", abstract)
	if err != nil {
		t.Fatal(err)
	}
	defer aln.Close()
	go func() {
		for {
			c, err := aln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	cmd := exec.Command(os.Args[0], "-test.run=TestSandboxHelper")
	cmd.Env = append(os.Environ(),
		"SANDBOX_HELPER=1",
		"SANDBOX_HOME="+home,
		"SANDBOX_WT="+wt,
		"SANDBOX_DECOY="+decoy,
		"SANDBOX_OUTSIDE="+outside,
		"SANDBOX_SOCK="+sock,
		"SANDBOX_ABSTRACT="+abstract,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper: %v\n%s", err, out)
	}
	for _, want := range []string{
		"write_worktree=true",
		"read_decoy=false",
		"write_outside=false",
		"connect_socket=false",
		"connect_abstract=false",
		"signal_outside=false",
		"luvus_env=false",
		"tmpdir_private=true",
	} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("helper output lacks %q:\n%s", want, out)
		}
	}
}

func TestSandboxHelper(t *testing.T) {
	if os.Getenv("SANDBOX_HELPER") == "" {
		t.Skip("helper for TestBoxedDeniesWhatLiesOutsideThePolicy")
	}
	wt := os.Getenv("SANDBOX_WT")
	p, err := Resolve(Request{
		Mode:     Boxed,
		Worktree: wt,
		Home:     os.Getenv("SANDBOX_HOME"),
		Getenv:   os.Getenv,
		Environ:  append(os.Environ(), "LUVUS_HOME=/run/luvus"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(p); err != nil {
		t.Fatal(err)
	}
	writeErr := os.WriteFile(filepath.Join(wt, "own.txt"), []byte("x"), 0o600)
	fmt.Printf("write_worktree=%v\n", writeErr == nil)
	_, readErr := os.ReadFile(os.Getenv("SANDBOX_DECOY"))
	fmt.Printf("read_decoy=%v\n", readErr == nil)
	outsideErr := os.WriteFile(os.Getenv("SANDBOX_OUTSIDE"), []byte("x"), 0o600)
	fmt.Printf("write_outside=%v\n", outsideErr == nil)
	conn, err := net.Dial("unix", os.Getenv("SANDBOX_SOCK"))
	if err == nil {
		conn.Close()
	}
	fmt.Printf("connect_socket=%v\n", err == nil)
	abstractConn, err := net.Dial("unix", os.Getenv("SANDBOX_ABSTRACT"))
	if err == nil {
		abstractConn.Close()
	}
	fmt.Printf("connect_abstract=%v\n", err == nil)
	fmt.Printf("signal_outside=%v\n", syscall.Kill(os.Getppid(), syscall.SIGCONT) == nil)
	_, luvus := envLookup(p.Environ, "LUVUS_HOME")
	fmt.Printf("luvus_env=%v\n", luvus)
	fmt.Printf("tmpdir_private=%v\n", envGet(p.Environ, "TMPDIR") == p.Temp)
}

func envGet(env []string, name string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v
		}
	}
	return ""
}

func envLookup(env []string, name string) (string, bool) {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}
