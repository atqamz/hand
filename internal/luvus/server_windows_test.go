package luvus_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/luvus"
)

func TestStartServerGivesTheWorkersGitLongPaths(t *testing.T) {
	out := filepath.Join(t.TempDir(), "env")
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "GIT_CONFIG_") {
			env = append(env, kv)
		}
	}
	bin := fakebin.Install(t, t.TempDir(), "luvus", "env", map[string]string{"out": out})
	if err := luvus.StartServer(context.Background(), bin, "secondhand-f1", t.TempDir(), env); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(b)), "GIT_CONFIG_COUNT=1\nGIT_CONFIG_KEY_0=core.longpaths\nGIT_CONFIG_VALUE_0=true"; strings.ReplaceAll(got, "\r\n", "\n") != want {
		t.Fatalf("env = %q", got)
	}
}
