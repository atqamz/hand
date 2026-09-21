package cmd

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/atqamz/hand/internal/toolchain"
)

func TestNormalizeProjectHTTPSPreservesWhitespaceRewrite(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_EXEC_PATH", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GIT_CONFIG_COUNT", "0")
	const locator = "https://github.com/owner/repo.git"
	managed := toolchain.Runtime{GitPath: gitPath, GitBin: t.TempDir()}
	available, err := managed.GitTransportAvailable(context.Background(), "https")
	if err != nil || available {
		t.Fatalf("no-helper fixture = %t, %v", available, err)
	}
	for _, test := range []struct{ name, suffix string }{
		{"unchanged", ""}, {"space", " "}, {"tab", "\t"},
		{"carriage return", "\r"}, {"nonbreaking space", "\u00a0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			effective := locator + test.suffix
			t.Setenv("GIT_CONFIG_COUNT", "1")
			t.Setenv("GIT_CONFIG_KEY_0", "url."+effective+".insteadOf")
			t.Setenv("GIT_CONFIG_VALUE_0", locator)
			out, err := runRuntimeCore(context.Background(), managed, "git", "", "ls-remote", "--get-url", locator)
			if err != nil || string(out) != effective+"\n" && string(out) != effective+"\r\n" {
				t.Fatalf("real Git rewrite = %q, %v; want framed %q", out, err, effective)
			}
			got, normalized := normalizeProjectHTTPSForClone(managed, locator)
			if test.suffix == "" && string(out) == locator+"\n" {
				if got != "git@github.com:owner/repo.git" || !normalized {
					t.Fatalf("unchanged locator did not normalize: %q, %t", got, normalized)
				}
			} else if got != locator || normalized {
				t.Fatalf("whitespace rewrite was overridden: %q, normalized=%t", got, normalized)
			}
		})
	}
}
