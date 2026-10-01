package luvus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestARunningServerProbeIsBounded(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	bin, run := t.TempDir(), t.TempDir()
	for name, script := range map[string]string{"systemd-run": "#!/bin/sh\nexit 1\n", "systemctl": "#!/bin/sh\nexec " + sleep + " 30\n"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(run, "systemd"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run, "systemd", "private"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	defer func(d time.Duration) { serverProbe = d }(serverProbe)
	serverProbe = 200 * time.Millisecond
	began := time.Now()
	if _, ok := RunningServer(context.Background(), []string{"PATH=" + bin, "XDG_RUNTIME_DIR=" + run}, "secondhand-luvus-f0"); ok || time.Since(began) > 3*time.Second {
		t.Fatalf("ok=%v after %s", ok, time.Since(began))
	}
}

func TestWaitingForAUnitToUnloadIsBounded(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	bin, run := t.TempDir(), t.TempDir()
	for name, script := range map[string]string{
		"systemd-run": "#!/bin/sh\necho 'Unit secondhand-luvus-f1.service was already loaded or has a fragment file.' >&2\nexit 1\n",
		"systemctl":   "#!/bin/sh\ncase \"$*\" in *ExecStart*) echo '{ path=/usr/bin/luvus ; argv[]=/usr/bin/luvus ; }';; *LoadState*) exec " + sleep + " 30;; esac\nexit 0\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(run, "systemd"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run, "systemd", "private"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	defer func(d time.Duration) { unloadWait = d }(unloadWait)
	unloadWait = 300 * time.Millisecond
	began := time.Now()
	err = StartServer(context.Background(), filepath.Join(bin, "luvus"), "secondhand-f1", "secondhand-luvus-f1", t.TempDir(), []string{"PATH=" + bin, "XDG_RUNTIME_DIR=" + run})
	if err == nil || time.Since(began) > 5*time.Second {
		t.Fatalf("err=%v after %s", err, time.Since(began))
	}
}
