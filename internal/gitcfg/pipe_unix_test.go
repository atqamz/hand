//go:build unix

package gitcfg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGitReturnsWhenAChildHoldsThePipe(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nsleep 30 &\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	began := time.Now()
	if _, err := run(context.Background(), t.TempDir(), "status"); time.Since(began) > 5*time.Second {
		t.Fatalf("git = %v after %s", err, time.Since(began))
	}
}

func TestRunNeverPromptsForCredentials(t *testing.T) {
	out, err := Run(context.Background(), t.TempDir(), "-c", "alias.e=!echo $GIT_TERMINAL_PROMPT", "e")
	if err != nil || strings.TrimSpace(out) != "0" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestRunMakesSSHFailInsteadOfPrompting(t *testing.T) {
	conf := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(conf, []byte("[core]\n\tsshCommand = ssh -i key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, command, ssh, global, want string }{
		{"plain", "", "", os.DevNull, "ssh -o BatchMode=yes"},
		{"configured", "", "", conf, "ssh -i key -o BatchMode=yes"},
		{"command set", "ssh -p 1", "", conf, "ssh -p 1"},
		{"GIT_SSH set", "", "/bin/ssh", conf, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("GIT_CONFIG_GLOBAL", c.global)
			t.Setenv("GIT_SSH_COMMAND", c.command)
			t.Setenv("GIT_SSH", c.ssh)
			out, err := Run(context.Background(), t.TempDir(), "-c", "alias.e=!echo $GIT_SSH_COMMAND", "e")
			if err != nil || strings.TrimSpace(out) != c.want {
				t.Fatalf("out=%q err=%v", out, err)
			}
		})
	}
}
